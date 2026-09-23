#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repository_root"

database_url=${DATABASE_TEST_URL:?DATABASE_TEST_URL must identify an isolated PostgreSQL database}
api_port=${IDENQA_CAPTURE_WEB_CORE_PORT:-18081}
web_port=${IDENQA_CAPTURE_WEB_DEMO_PORT:-14173}
unset IDENQA_CAPTURE_WEB_CORE_PORT IDENQA_CAPTURE_WEB_DEMO_PORT
core_url="http://127.0.0.1:${api_port}"
demo_url="http://127.0.0.1:${web_port}"
runtime_directory=$(mktemp -d -t idenqa-capture-web.XXXXXX)
api_log="${runtime_directory}/api.log"
worker_log="${runtime_directory}/worker.log"
web_log="${runtime_directory}/web.log"
keyring_file="${runtime_directory}/evidence-keyring.json"
evidence_directory="${runtime_directory}/evidence"
mkdir "$evidence_directory"

# Fixed synthetic keys are confined to the caller-provided isolated database.
pepper=QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI
cursor_key=Q0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0NDQ0M
capture_key=REREREREREREREREREREREREREREREREREREREREREQ
outcome_key=RUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUVFRUU

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
export IDENQA_HEADGATE_INSTALLATION_ID=idenqa-capture-web-conformance
export IDENQA_HEADGATE_SCHEMA=headgate
export IDENQA_REGION=tenant-local
export IDENQA_REALTIME_WEBSOCKET_URL="ws://127.0.0.1:${api_port}/v1/capture/socket"
export IDENQA_HTTP_CORS_ALLOWED_ORIGINS=$demo_url
export IDENQA_HTTP_HOST=127.0.0.1
export IDENQA_HTTP_PORT=$api_port
export IDENQA_HTTP_REQUEST_TIMEOUT=5s
export IDENQA_SHUTDOWN_TIMEOUT=5s
export IDENQA_LOG_LEVEL=error
export IDENQA_EVIDENCE_LOCAL_DIRECTORY=$evidence_directory
export IDENQA_EVIDENCE_LOCAL_KEYRING_FILE=$keyring_file
export IDENQA_WORKER_SYNTHETIC_PROCESSING=true

api_pid=
worker_pid=
web_pid=
cleanup() {
  status=$?
  if [ -n "$web_pid" ]; then
    kill "$web_pid" 2>/dev/null || true
    wait "$web_pid" 2>/dev/null || true
  fi
  if [ -n "$worker_pid" ]; then
    kill "$worker_pid" 2>/dev/null || true
    wait "$worker_pid" 2>/dev/null || true
  fi
  if [ -n "$api_pid" ]; then
    kill "$api_pid" 2>/dev/null || true
    wait "$api_pid" 2>/dev/null || true
  fi
  if [ "$status" -ne 0 ]; then
    tail -n 80 "$api_log" >&2 2>/dev/null || true
    tail -n 80 "$worker_log" >&2 2>/dev/null || true
    tail -n 80 "$web_log" >&2 2>/dev/null || true
  fi
  rm -rf "$runtime_directory"
  trap - EXIT INT TERM
  exit "$status"
}
trap cleanup EXIT INT TERM

./bin/idenqa migrate up >/dev/null
./bin/idenqa migrate headgate up >/dev/null
./bin/idenqa evidence-key init --keyring-file "$keyring_file" >/dev/null

tenant_output=$(./bin/idenqa tenant create \
  --actor capture-web-conformance \
  --reason "create synthetic Capture Web tenant")
tenant_id=$(printf '%s\n' "$tenant_output" | sed -n 's/^tenant id=\([^ ]*\).*/\1/p')
if [ -z "$tenant_id" ]; then
  echo "could not parse tenant identifier from CLI output" >&2
  exit 1
fi

key_output=$(./bin/idenqa api-key create \
  --tenant "$tenant_id" \
  --label capture-web-conformance \
  --scope 'capture_profiles:*' \
  --scope 'notices:*' \
  --scope 'policies:*' \
  --scope 'reviews:*' \
  --scope 'verification_sessions:*' \
  --scope 'authorities:*' \
  --expires-at 2099-01-01T00:00:00Z \
  --actor capture-web-conformance \
  --reason "create synthetic Capture Web credential")
api_key=$(printf '%s\n' "$key_output" | sed -n 's/.* credential=\([^ ]*\)$/\1/p')
api_key_id=$(printf '%s\n' "$key_output" | sed -n 's/^api_key id=\([^ ]*\).*/\1/p')
if [ -z "$api_key" ] || [ -z "$api_key_id" ]; then
  echo "could not parse API-key identifier and display-once credential from CLI output" >&2
  exit 1
fi

IDENQA_DATABASE_ADMIN_URL= ./bin/api >"$api_log" 2>&1 &
api_pid=$!

attempt=0
until curl --fail --silent --show-error "${core_url}/readyz" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 50 ]; then
    echo "Core API did not become ready" >&2
    tail -n 80 "$api_log" >&2
    exit 1
  fi
  sleep 0.2
done

IDENQA_DATABASE_ADMIN_URL= ./bin/worker >"$worker_log" 2>&1 &
worker_pid=$!

corepack pnpm --dir capture/web build
corepack pnpm --dir capture/web assets:prepare

IDENQA_DEMO_CORE_URL=$core_url \
IDENQA_DEMO_CONFORMANCE=true \
IDENQA_DEMO_TENANT_API_KEY=$api_key \
IDENQA_DEMO_TENANT_API_KEY_ID=$api_key_id \
IDENQA_DEMO_TENANT_ID=$tenant_id \
IDENQA_DEMO_REGION=$IDENQA_REGION \
  pnpm --dir capture/web exec vite ./demo --config ./vite.config.mjs --host 127.0.0.1 --port "$web_port" --strictPort >"$web_log" 2>&1 &
web_pid=$!

attempt=0
until curl --fail --silent --show-error "${demo_url}/hosted.html" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 50 ]; then
    echo "Capture Web demo did not become ready" >&2
    tail -n 80 "$web_log" >&2
    exit 1
  fi
  sleep 0.2
done

IDENQA_CAPTURE_LIVE_DEMO_URL=$demo_url \
  pnpm --dir capture/web exec playwright test test/browser/real-core.spec.ts "$@"
