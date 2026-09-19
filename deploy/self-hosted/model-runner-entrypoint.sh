#!/bin/sh
# Isolated ONNX model runner entrypoint.
#
# The provision service generates the deployment-local CA, server certificate,
# and runner credentials. This entrypoint waits for those artifacts, writes the
# evaluation-only fixture configuration once, and then serves the pinned
# private TLS runner.
set -eu

runtime_directory=/run/idenqa
tls_directory=/run/idenqa/tls
runner_directory=/run/idenqa/runner

tries=0
until [ -f "$tls_directory/ca.crt" ] && [ -f "$tls_directory/model-runner.crt" ] &&
	[ -f "$runner_directory/model_credential" ] && [ -f "$runner_directory/model_gateway_credential" ]; do
	tries=$((tries + 1))
	if [ "$tries" -gt 300 ]; then
		echo "model-runner: waiting for provisioned artifacts timed out" >&2
		exit 1
	fi
	sleep 1
done

if [ ! -f "$runtime_directory/model-runner.json" ]; then
	model-runner-fixture \
		--output "$runtime_directory/model-runner.json" \
		--python "$(command -v python3)" \
		--model-file /opt/idenqa/models/pad_fixture.onnx \
		--listen-address 0.0.0.0:9091 \
		--certificate-file "$tls_directory/model-runner.crt" \
		--private-key-file "$tls_directory/model-runner.key" \
		--credential-file "$runner_directory/model_credential" \
		--gateway-url https://api:8443 \
		--gateway-ca-file "$tls_directory/ca.crt" \
		--gateway-credential-file "$runner_directory/model_gateway_credential"
fi

exec model-runner --config "$runtime_directory/model-runner.json"
