#!/bin/sh
# Static Capture Web fixture entrypoint. Session bootstrap belongs to a tenant
# backend or the managed launch gateway; this process owns no tenant authority.
set -eu

exec /app/capture/web/node_modules/.bin/vite ./demo \
	--config ./vite.config.mjs \
	--host 0.0.0.0 \
	--port 4173 \
	--strictPort
