#!/bin/sh
# Common process entrypoint for the self-hosted compose stack.
#
# The provision service writes deployment-local development credentials to the
# shared runtime volume. They are sourced here so no secret is committed to the
# repository or embedded in the compose file. Process arguments are executed
# unchanged as PID 1.
set -eu

if [ -f /run/idenqa/runtime.env ]; then
	set -a
	# shellcheck disable=SC1091
	. /run/idenqa/runtime.env
	set +a
fi

exec "$@"
