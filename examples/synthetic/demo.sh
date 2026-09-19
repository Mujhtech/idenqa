#!/bin/sh
# Runs the packaged synthetic demonstration against a local Core deployment.
#
# Prerequisites:
#   - ./bin/idenqa exists (go build -o bin/idenqa ./cmd/idenqa);
#   - PostgreSQL is reachable through the deployment environment or .env;
#   - the API is running at IDENQA_DEMO_API_URL;
#   - the worker is running with IDENQA_WORKER_SYNTHETIC_PROCESSING=true so the
#     synthetic checks can execute and author the decision.
#
# The script creates a fresh tenant and display-once API key, writes the
# credential to an owner-only temporary file, and removes it on exit. It never
# embeds or prints a secret.
set -euo pipefail

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repository_root"

api_url=${IDENQA_DEMO_API_URL:-http://127.0.0.1:8080}
profile_file=examples/synthetic/capture-profile.json
policy_file=examples/synthetic/policy.json
idempotency_prefix=synthetic

if [ ! -x ./bin/idenqa ]; then
  echo "build the CLI first: go build -o bin/idenqa ./cmd/idenqa" >&2
  exit 1
fi

runtime_directory=$(mktemp -d -t idenqa-synthetic-demo.XXXXXX)
key_file="${runtime_directory}/api-key"
cleanup() {
  rm -rf "$runtime_directory"
}
trap cleanup EXIT INT TERM

echo "==> migrate"
./bin/idenqa migrate up
./bin/idenqa migrate headgate up

echo "==> create tenant and tenant API key"
tenant_output=$(./bin/idenqa tenant create \
  --actor synthetic-demo \
  --reason "create synthetic demonstration tenant")
tenant_id=$(printf '%s\n' "$tenant_output" | sed -n 's/^tenant id=\([^ ]*\).*/\1/p')
if [ -z "$tenant_id" ]; then
  echo "could not parse the tenant identifier from CLI output" >&2
  exit 1
fi
key_output=$(./bin/idenqa api-key create \
  --tenant "$tenant_id" \
  --label synthetic-demo \
  --scope 'capture_profiles:*' \
  --scope 'decisions:read' \
  --scope 'notices:*' \
  --scope 'policies:*' \
  --scope 'tenant:read' \
  --scope 'verification_sessions:*' \
  --scope 'authorities:*' \
  --expires-at 2099-01-01T00:00:00Z \
  --actor synthetic-demo \
  --reason "create synthetic demonstration credential")
credential=$(printf '%s\n' "$key_output" | sed -n 's/.* credential=\([^ ]*\)$/\1/p')
if [ -z "$credential" ]; then
  echo "could not parse the display-once API credential from CLI output" >&2
  exit 1
fi
umask 077
printf '%s\n' "$credential" > "$key_file"
unset credential key_output

echo "==> doctor"
./bin/idenqa doctor --api-url "$api_url" --api-key-file "$key_file"

echo "==> create and activate the packaged example policy"
policy_output=$(./bin/idenqa policy create \
  --api-url "$api_url" \
  --api-key-file "$key_file" \
  --file "$policy_file" \
  --idempotency-key "${idempotency_prefix}-policy-create")
policy_id=$(printf '%s\n' "$policy_output" | sed -n 's/.*"id":"\(pol_[^"]*\)".*/\1/p')
if [ -z "$policy_id" ]; then
  echo "could not parse the policy identifier from CLI output" >&2
  exit 1
fi
./bin/idenqa policy activate \
  --api-url "$api_url" \
  --api-key-file "$key_file" \
  --id "$policy_id" \
  --revision 1 \
  --expected-version 0 \
  --reason synthetic_journey \
  --idempotency-key "${idempotency_prefix}-policy-activate" \
  --confirm

echo "==> synthetic journey"
./bin/idenqa synthetic run \
  --api-url "$api_url" \
  --api-key-file "$key_file" \
  --profile-file "$profile_file" \
  --policy-file "$policy_file" \
  --idempotency-prefix "$idempotency_prefix"
