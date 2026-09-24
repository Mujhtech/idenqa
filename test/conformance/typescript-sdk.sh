#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repository_root"

database_url=${DATABASE_TEST_URL:?DATABASE_TEST_URL must identify an isolated PostgreSQL database}
api_port=${IDENQA_SDK_CONFORMANCE_PORT:-18080}
unset IDENQA_SDK_CONFORMANCE_PORT
base_url="http://127.0.0.1:${api_port}"

# Webhook event bodies are wrapped with the evidence KEK, so the conformance
# deployment provisions an isolated local keyring and ciphertext directory.
runtime_directory=$(mktemp -d -t idenqa-sdk-conformance.XXXXXX)
keyring_file="$runtime_directory/keyring.json"
mkdir -p "$runtime_directory/evidence"
./bin/idenqa evidence-key init --keyring-file "$keyring_file" >/dev/null

# Fixed synthetic keys are confined to this isolated conformance deployment.
pepper=QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI
cursor_key=Q0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0M
capture_key=REREREREREREREREREREREREREREREREREREREREREQ
outcome_key=U0ZGRkZGRkZGRkZGRkZGRkZGRkZGRkZGRkZGRkZGRkY

export IDENQA_ENVIRONMENT=test
export IDENQA_DATABASE_URL=$database_url
export IDENQA_DATABASE_ADMIN_URL=$database_url
export IDENQA_API_KEY_ACTIVE_PEPPER_VERSION=1
export IDENQA_API_KEY_PEPPERS="1=${pepper}"
export IDENQA_API_KEY_ALLOW_NO_EXPIRY=false
export IDENQA_API_KEY_MAXIMUM_LIFETIME=1000000h
export IDENQA_API_KEY_MAXIMUM_ROTATION_OVERLAP=24h
export IDENQA_CURSOR_ACTIVE_KEY_VERSION=1
export IDENQA_CURSOR_KEYS="1=${cursor_key}"
export IDENQA_CAPTURE_TOKEN_ACTIVE_KEY_VERSION=1
export IDENQA_CAPTURE_TOKEN_KEYS="1=${capture_key}"
export IDENQA_OUTCOME_TOKEN_ACTIVE_KEY_VERSION=1
export IDENQA_OUTCOME_TOKEN_KEYS="1=${outcome_key}"
export IDENQA_HTTP_HOST=127.0.0.1
export IDENQA_HTTP_PORT=$api_port
export IDENQA_LOG_LEVEL=error
export IDENQA_REGION=local
export IDENQA_REALTIME_WEBSOCKET_URL="ws://127.0.0.1:${api_port}/v1/capture/socket"
export IDENQA_HTTP_CORS_ALLOWED_ORIGINS="http://127.0.0.1:${api_port}"
export IDENQA_EVIDENCE_LOCAL_DIRECTORY="$runtime_directory/evidence"
export IDENQA_EVIDENCE_LOCAL_KEYRING_FILE=$keyring_file

./bin/idenqa migrate up >/dev/null

tenant_output=$(./bin/idenqa tenant create \
  --actor sdk-conformance \
  --reason "create synthetic SDK conformance tenant")
tenant_id=$(printf '%s\n' "$tenant_output" | sed -n 's/^tenant id=\([^ ]*\).*/\1/p')
if [ -z "$tenant_id" ]; then
  echo "could not parse tenant identifier from CLI output" >&2
  exit 1
fi

key_output=$(./bin/idenqa api-key create \
  --tenant "$tenant_id" \
  --label sdk-conformance \
  --scope 'capture_profiles:*' \
  --scope 'verification_sessions:*' \
  --scope 'policies:*' \
  --expires-at 2099-01-01T00:00:00Z \
  --actor sdk-conformance \
  --reason "create synthetic SDK conformance credential")
api_key=$(printf '%s\n' "$key_output" | sed -n 's/.* credential=\([^ ]*\)$/\1/p')
if [ -z "$api_key" ]; then
  echo "could not parse display-once API credential from CLI output" >&2
  exit 1
fi

api_log=$(mktemp -t idenqa-sdk-conformance.XXXXXX)
api_pid=
cleanup() {
  if [ -n "$api_pid" ]; then
    kill "$api_pid" 2>/dev/null || true
    wait "$api_pid" 2>/dev/null || true
  fi
  rm -f "$api_log"
  rm -rf "$runtime_directory"
}
trap cleanup EXIT INT TERM

IDENQA_DATABASE_ADMIN_URL= ./bin/api >"$api_log" 2>&1 &
api_pid=$!

attempt=0
until curl --fail --silent --show-error "${base_url}/readyz" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 50 ]; then
    echo "API did not become ready" >&2
    tail -n 80 "$api_log" >&2
    exit 1
  fi
  sleep 0.2
done

IDENQA_SDK_CONFORMANCE_BASE_URL=$base_url \
IDENQA_SDK_CONFORMANCE_API_KEY=$api_key \
  corepack pnpm --filter @idenqa/sdk test
