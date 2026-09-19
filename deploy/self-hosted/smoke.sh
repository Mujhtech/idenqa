#!/usr/bin/env bash
# Self-hosted clean-usability gate (gap audit section 26).
#
# Runs the packaged open-source deployment end to end without Cloud or Console
# and prints one PASS/FAIL line per gate step. Every wait is bounded and every
# command runs inside the packaged images; the host only needs Docker Compose.
#
# Usage:
#   deploy/self-hosted/smoke.sh
#
# The script leaves the stack running for inspection. It creates one fresh
# tenant per run and stores the display-once credential in the shared state
# volume at /state/api-key (owner-only, development only).
set -euo pipefail

script_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
compose_file="$script_directory/compose.yaml"
run_id="$(date +%Y%m%d%H%M%S)-$$"
gate_complete=0

compose() {
	docker compose --project-directory "$script_directory" -f "$compose_file" "$@"
}

# Run a packaged binary inside the API container with the generated credential
# environment sourced by the common entrypoint.
api_exec() {
	compose exec -T api idenqa-entrypoint "$@"
}

api_cli() {
	api_exec idenqa "$@"
}

current_step=""
step_begin() {
	current_step=$1
	printf 'STEP %02d %-52s ... ' "$1" "$2"
}

step_pass() {
	if [ "$#" -gt 0 ] && [ -n "$1" ]; then
		printf 'PASS (%s)\n' "$1"
	else
		printf 'PASS\n'
	fi
}

step_fail() {
	printf 'FAIL: %s\n' "$1"
	exit 1
}

trap 'status=$?; if [ "$gate_complete" -eq 0 ]; then printf "\nGate stopped at STEP %02d; stack left running for inspection.\n" "${current_step:-0}"; fi; exit $status' EXIT

# --- 1. start core without Cloud or Console ---------------------------------
step_begin 1 "start core without Cloud or Console"
if ! compose up -d --wait postgres minio >/dev/null 2>&1; then
	step_fail "PostgreSQL and MinIO did not become healthy"
fi
if ! compose run --rm minio-init >/dev/null 2>&1; then
	step_fail "evidence bucket initialisation failed"
fi
if ! compose up -d --wait api worker adapter-runner webhook-receiver >/dev/null 2>&1; then
	step_fail "core services did not become healthy"
fi
step_pass "postgres, minio, api, worker, adapter-runner, webhook-receiver"

# The deployment region must match between synthetic capture, evidence, and
# privacy deletion. Read it from the running API instead of duplicating the
# compose default.
gate_region=$(compose exec -T api printenv IDENQA_REGION 2>/dev/null | tr -d '\r\n') || gate_region=""
if [ -z "$gate_region" ]; then
	gate_region=tenant-local
fi

# --- 2. run migrations -------------------------------------------------------
step_begin 2 "run application and Headgate migrations"
if ! migration_output=$(compose run --rm migrate 2>&1); then
	step_fail "migration command failed: $migration_output"
fi
if ! printf '%s\n' "$migration_output" | grep -q 'pending=false'; then
	step_fail "migrations did not leave a clean schema: $migration_output"
fi
step_pass "schema and Headgate migrations applied"

# --- 3. create tenant and scoped credential ---------------------------------
step_begin 3 "create tenant and scoped credential"
if ! tenant_output=$(api_cli tenant create --actor self-hosted-smoke --reason "self-hosted smoke gate" 2>&1); then
	step_fail "tenant creation failed: $tenant_output"
fi
tenant_id=$(printf '%s\n' "$tenant_output" | sed -n 's/^tenant id=\([^ ]*\).*/\1/p')
if [ -z "$tenant_id" ]; then
	step_fail "tenant identifier was not returned: $tenant_output"
fi
if ! key_output=$(api_cli api-key create \
	--tenant "$tenant_id" \
	--label self-hosted-smoke \
	--scope 'capture_profiles:*' \
	--scope 'policies:*' \
	--scope 'notices:*' \
	--scope 'verification_sessions:*' \
	--scope 'authorities:*' \
	--scope 'decisions:read' \
	--scope 'webhooks:configure' \
	--scope 'webhooks:read' \
	--scope 'deletions:read' \
	--scope 'deletions:write' \
	--scope 'tenant:read' \
	--scope 'tenant:export' \
	--expires-at 2099-01-01T00:00:00Z \
	--actor self-hosted-smoke --reason "self-hosted smoke gate" 2>&1); then
	step_fail "credential creation failed: $key_output"
fi
credential=$(printf '%s\n' "$key_output" | sed -n 's/.* credential=\([^ ]*\).*/\1/p')
if [ -z "$credential" ]; then
	step_fail "display-once credential was not returned"
fi
if ! printf '%s' "$credential" | compose exec -T api sh -c 'umask 077; cat > /state/api-key'; then
	step_fail "could not store the tenant credential in the state volume"
fi
unset credential key_output
step_pass "tenant=$tenant_id"

# --- 4. activate the packaged example policy --------------------------------
step_begin 4 "activate the packaged example policy"
if ! policy_output=$(api_cli policy create \
	--api-url http://127.0.0.1:8080 \
	--api-key-file /state/api-key \
	--file /opt/idenqa/examples/synthetic/policy.json \
	--idempotency-key "smoke-$run_id-policy-create" 2>&1); then
	step_fail "policy creation failed: $policy_output"
fi
policy_id=$(printf '%s' "$policy_output" | sed -n 's/.*"id":"\(pol_[^"]*\)".*/\1/p')
if [ -z "$policy_id" ]; then
	step_fail "policy identifier was not returned: $policy_output"
fi
if ! activate_output=$(api_cli policy activate \
	--api-url http://127.0.0.1:8080 \
	--api-key-file /state/api-key \
	--id "$policy_id" \
	--revision 1 \
	--expected-version 0 \
	--reason synthetic_journey \
	--idempotency-key "smoke-$run_id-policy-activate" \
	--confirm 2>&1); then
	step_fail "policy activation failed: $activate_output"
fi
step_pass "policy=$policy_id revision=1"

# --- 5. install or use a supported SDK / CLI --------------------------------
step_begin 5 "use the packaged CLI and SDK"
if ! version_output=$(api_cli version 2>&1); then
	step_fail "CLI version command failed: $version_output"
fi
if ! doctor_output=$(api_cli doctor --api-url http://127.0.0.1:8080 --api-key-file /state/api-key 2>&1); then
	step_fail "doctor preflight failed: $doctor_output"
fi
if ! printf '%s\n' "$doctor_output" | grep -q 'doctor ok'; then
	step_fail "doctor reported failures: $doctor_output"
fi
if ! compose up -d --wait capture-web >/dev/null 2>&1; then
	step_fail "capture-web did not serve the hosted journey"
fi
if ! bootstrap_status=$(compose exec -T capture-web curl -sS -o /dev/null -w '%{http_code}' \
	-X POST -H 'Sec-Fetch-Site: same-origin' \
	http://127.0.0.1:4173/__idenqa_demo/bootstrap 2>&1); then
	step_fail "capture-web SDK bootstrap request failed: $bootstrap_status"
fi
if [ "$bootstrap_status" != "201" ]; then
	step_fail "capture-web SDK bootstrap returned status $bootstrap_status"
fi
step_pass "$version_output; capture-web serves /hosted.html and provisions a real Core journey through the SDK"

# --- 6. complete a synthetic capture journey --------------------------------
step_begin 6 "complete a synthetic capture journey"
compose exec -T api sh -c 'rm -f /state/webhook-secret' >/dev/null 2>&1 || true
if ! webhook_output=$(api_cli webhook create \
	--api-url http://127.0.0.1:8080 \
	--api-key-file /state/api-key \
	--url https://webhook-receiver/webhooks/idenqa \
	--event-types verification.completed \
	--schema-version 1.0 \
	--idempotency-key "smoke-$run_id-webhook" \
	--secret-out /state/webhook-secret 2>&1); then
	step_fail "webhook endpoint creation failed: $webhook_output"
fi
endpoint_id=$(printf '%s' "$webhook_output" | sed -n 's/.*"id":"\(whk_[^"]*\)".*/\1/p')
if [ -z "$endpoint_id" ]; then
	step_fail "webhook endpoint identifier was not returned: $webhook_output"
fi
if ! journey_output=$(api_cli synthetic run \
	--api-url http://127.0.0.1:8080 \
	--api-key-file /state/api-key \
	--profile-file /opt/idenqa/examples/synthetic/capture-profile.json \
	--policy-file /opt/idenqa/examples/synthetic/policy.json \
	--idempotency-prefix "smoke-$run_id" \
	--region "$gate_region" \
	--timeout 120s 2>&1); then
	step_fail "synthetic journey failed: $journey_output"
fi
verification_id=$(printf '%s\n' "$journey_output" | sed -n 's/.*verification_id=\([^ ]*\).*/\1/p' | tail -n 1)
decision_id=$(printf '%s\n' "$journey_output" | sed -n 's/.*decision_id=\([^ ]*\).*/\1/p' | tail -n 1)
if [ -z "$verification_id" ] || [ -z "$decision_id" ]; then
	step_fail "journey did not report a verification and decision: $journey_output"
fi
step_pass "verification=$verification_id decision=$decision_id"

# --- 7. execute checks -------------------------------------------------------
step_begin 7 "execute checks"
if ! assurance_output=$(api_cli assurance verification "$verification_id" \
	--api-url http://127.0.0.1:8080 \
	--api-key-file /state/api-key 2>&1); then
	step_fail "assurance projection failed: $assurance_output"
fi
if [ -z "$assurance_output" ]; then
	step_fail "assurance projection was empty"
fi
step_pass "worker-executed checks are projected"

# --- 8. produce and reproduce a decision ------------------------------------
step_begin 8 "produce and reproduce a decision"
if ! decision_bundle=$(api_cli policy decision reproduce --tenant "$tenant_id" --id "$decision_id" --output bundle 2>&1); then
	step_fail "decision reproduction failed: $decision_bundle"
fi
if ! verify_output=$(printf '%s' "$decision_bundle" | api_cli policy decision verify --bundle-file - --output summary 2>&1); then
	step_fail "offline decision verification failed: $verify_output"
fi
if ! printf '%s\n' "$verify_output" | grep -q 'policy_decision_reproduced'; then
	step_fail "decision bundle did not verify: $verify_output"
fi
step_pass "$verify_output"

# --- 9. deliver and independently verify a signed webhook -------------------
step_begin 9 "deliver and independently verify a signed webhook"
waited=0
while [ "$waited" -lt 45 ]; do
	if compose exec -T api sh -c 'grep -q "\"verified\":true" /state/verified-events.ndjson 2>/dev/null'; then
		break
	fi
	waited=$((waited + 1))
	sleep 2
done
if [ "$waited" -ge 45 ]; then
	step_fail "no verified webhook was recorded within 90 seconds"
fi
record=$(compose exec -T api sh -c 'grep "\"verified\":true" /state/verified-events.ndjson | tail -n 1' 2>/dev/null)
if ! printf '%s\n' "$record" | grep -q '"type":"verification.completed"'; then
	step_fail "unexpected webhook record: $record"
fi
step_pass "sdk/go verified event: $record"

# --- 10. inspect the audit chain --------------------------------------------
step_begin 10 "inspect the audit chain"
if ! export_output=$(api_exec audit-export \
	--tenant "$tenant_id" \
	--export-file /state/audit-export.json \
	--keys-file /state/audit-keys.json 2>&1); then
	step_fail "audit export failed: $export_output"
fi
if ! audit_output=$(api_cli audit verify --export-file /state/audit-export.json --keys-file /state/audit-keys.json 2>&1); then
	step_fail "audit verification failed: $audit_output"
fi
if ! printf '%s\n' "$audit_output" | grep -q '"last_sequence"'; then
	step_fail "audit verification returned no report: $audit_output"
fi
step_pass "$export_output"

# --- 11. simulate provider failure and recovery -----------------------------
step_begin 11 "simulate provider failure and recovery"
if ! compose stop worker >/dev/null 2>&1; then
	step_fail "could not stop the processing worker"
fi
if recovery_output=$(api_cli synthetic run \
	--api-url http://127.0.0.1:8080 \
	--api-key-file /state/api-key \
	--profile-file /opt/idenqa/examples/synthetic/capture-profile.json \
	--policy-file /opt/idenqa/examples/synthetic/policy.json \
	--idempotency-prefix "smoke-recovery-$run_id" \
	--region "$gate_region" \
	--timeout 10s 2>&1); then
	compose up -d --wait worker >/dev/null 2>&1 || true
	step_fail "journey completed while the processing worker was stopped"
fi
recovery_verification=$(printf '%s\n' "$recovery_output" | sed -n 's/.*verification \([^ ]*\) did not reach.*/\1/p')
if ! compose up -d --wait worker >/dev/null 2>&1; then
	step_fail "worker did not restart"
fi
recovered_state=""
if [ -n "$recovery_verification" ]; then
	waited=0
	while [ "$waited" -lt 45 ]; do
		recovered_state=$(compose exec -T api sh -c "curl -fsS -H \"Authorization: Bearer \$(cat /state/api-key)\" http://127.0.0.1:8080/v1/verifications/$recovery_verification" 2>/dev/null | grep -o '"state":"[a-z_]*"' | head -n 1 | sed 's/"state":"\([a-z_]*\)"/\1/') || recovered_state=""
		if [ "$recovered_state" = "completed" ]; then
			break
		fi
		waited=$((waited + 1))
		sleep 2
	done
	if [ "$recovered_state" != "completed" ]; then
		step_fail "interrupted verification did not recover (state=$recovered_state)"
	fi
	step_pass "interrupted $recovery_verification recovered to completed"
else
	step_pass "worker restart restored processing"
fi

# --- 12. run retention and deletion -----------------------------------------
step_begin 12 "run retention and deletion"
if ! retention_output=$(api_cli privacy retention \
	--api-url http://127.0.0.1:8080 \
	--api-key-file /state/api-key \
	--aggregate-id "$verification_id" 2>&1); then
	step_fail "retention resolution failed: $retention_output"
fi
deletion_body="{\"aggregate_id\":\"$verification_id\",\"region\":\"$gate_region\"}"
if ! deletion_output=$(compose exec -T api sh -c \
	'curl -fsS -X POST -H "Authorization: Bearer $(cat /state/api-key)" -H "Content-Type: application/json" -d "$1" http://127.0.0.1:8080/v1/deletions' \
	sh "$deletion_body" 2>&1); then
	step_fail "deletion request failed: $deletion_output"
fi
deletion_id=$(printf '%s' "$deletion_output" | sed -n 's/.*"id":"\(del_[^"]*\)".*/\1/p')
if [ -z "$deletion_id" ]; then
	step_fail "deletion identifier was not returned: $deletion_output"
fi
deletion_state=""
waited=0
while [ "$waited" -lt 60 ]; do
	deletion_status=$(api_cli privacy deletion "$deletion_id" \
		--api-url http://127.0.0.1:8080 \
		--api-key-file /state/api-key 2>&1) || deletion_status=""
	deletion_state=$(printf '%s' "$deletion_status" | grep -o '"state":"[a-z_]*"' | head -n 1 | sed 's/"state":"\([a-z_]*\)"/\1/')
	case "$deletion_state" in
	awaiting_backup | awaiting_backup_expiry | completed)
		break
		;;
	esac
	waited=$((waited + 1))
	sleep 2
done
case "$deletion_state" in
awaiting_backup | awaiting_backup_expiry | completed)
	step_pass "deletion=$deletion_id state=$deletion_state"
	;;
*)
	step_fail "deletion did not erase targets (state=$deletion_state)"
	;;
esac

# --- 13. export tenant-owned data -------------------------------------------
step_begin 13 "export tenant-owned data and verify the digest"
if ! export_tenant_output=$(api_cli export tenant \
	--api-url http://127.0.0.1:8080 \
	--api-key-file /state/api-key \
	--output /state/tenant-export.ndjson \
	--force 2>&1); then
	step_fail "tenant export failed: $export_tenant_output"
fi
if ! digest_output=$(compose exec -T api sh -c '
file=/state/tenant-export.ndjson
footer=$(tail -n 1 "$file")
digest=$(printf "%s" "$footer" | sed -n "s/.*\"digest\":\"sha256:\([0-9a-f]*\)\".*/\1/p")
computed=$(head -n -1 "$file" | sha256sum | cut -d" " -f1)
if [ -n "$digest" ] && [ "$digest" = "$computed" ]; then
	printf "export_digest_ok lines=%s" "$(wc -l < "$file")"
else
	printf "digest mismatch footer=%s computed=%s" "$digest" "$computed"
	exit 1
fi' 2>&1); then
	step_fail "$digest_output"
fi
step_pass "$digest_output"

gate_complete=1
printf '\nAll 13 self-hosted gate steps passed.\n'
printf 'tenant=%s verification=%s decision=%s\n' "$tenant_id" "$verification_id" "$decision_id"
printf 'The stack remains running; use `docker compose --project-directory %s -f %s down -v` to remove it.\n' \
	"$script_directory" "$compose_file"
