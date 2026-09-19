#!/bin/sh
# Capture Web demo entrypoint.
#
# The documented server-side bootstrap keeps the tenant API key outside the
# browser. The stack creates that credential during the smoke gate, so this
# entrypoint waits a bounded time for the shared state file and then starts the
# documented Vite demo server that owns the bootstrap middleware.
set -eu

tries=0
until [ -s /state/api-key ]; do
	tries=$((tries + 1))
	if [ "$tries" -gt 300 ]; then
		echo "capture-web: waiting for /state/api-key timed out" >&2
		exit 1
	fi
	sleep 1
done

IDENQA_DEMO_TENANT_API_KEY="$(cat /state/api-key)"
export IDENQA_DEMO_TENANT_API_KEY
export IDENQA_DEMO_CORE_URL="${IDENQA_DEMO_CORE_URL:-http://api:8080}"
export IDENQA_DEMO_REGION="${IDENQA_DEMO_REGION:-tenant-local}"

exec /app/capture/web/node_modules/.bin/vite ./demo \
	--config ./vite.config.mjs \
	--host 0.0.0.0 \
	--port 4173 \
	--strictPort
