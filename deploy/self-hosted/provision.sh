#!/bin/sh
# Development-only secret, TLS, and fixture provisioning for the self-hosted
# compose stack. This service is idempotent: it generates each artifact only
# when it is missing, so restarts never rotate deployment-local credentials.
#
# Nothing written here belongs in production. Replace the file keyring with the
# selected KMS and the local CA with the deployment certificate authority before
# operating a real installation.
set -eu
umask 077

runtime_directory=/run/idenqa
tls_directory=/run/idenqa/tls
runner_directory=/run/idenqa/runner
state_directory=/state
evidence_directory=/var/lib/idenqa/evidence

mkdir -p "$runtime_directory" "$tls_directory" "$runner_directory" "$state_directory" "$evidence_directory"

generate_key() {
	openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n'
}

# --- deployment-local development secrets -----------------------------------
if [ ! -f "$runtime_directory/runtime.env" ]; then
	cat > "$runtime_directory/runtime.env" <<EOF
IDENQA_API_KEY_ACTIVE_PEPPER_VERSION=1
IDENQA_API_KEY_PEPPERS=1=$(generate_key)
IDENQA_CURSOR_ACTIVE_KEY_VERSION=1
IDENQA_CURSOR_KEYS=1=$(generate_key)
IDENQA_CAPTURE_TOKEN_ACTIVE_KEY_VERSION=1
IDENQA_CAPTURE_TOKEN_KEYS=1=$(generate_key)
IDENQA_OUTCOME_TOKEN_ACTIVE_KEY_VERSION=1
IDENQA_OUTCOME_TOKEN_KEYS=1=$(generate_key)
EOF
	chmod 0600 "$runtime_directory/runtime.env"
fi

# --- local evidence KEK keyring ---------------------------------------------
if [ ! -f "$runtime_directory/evidence-keyring.json" ]; then
	idenqa evidence-key init --keyring-file "$runtime_directory/evidence-keyring.json" >/dev/null
fi

# --- development certificate authority --------------------------------------
if [ ! -f "$tls_directory/ca.crt" ] || [ ! -f "$tls_directory/ca.key" ]; then
	openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 3650 \
		-keyout "$tls_directory/ca.key" -out "$tls_directory/ca.crt" \
		-subj "/CN=idenqa-self-hosted-dev-ca" \
		-addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1
fi

issue_server_certificate() {
	name=$1
	if [ -f "$tls_directory/$name.crt" ] && [ -f "$tls_directory/$name.key" ]; then
		return
	fi
	openssl req -new -newkey rsa:2048 -nodes -sha256 \
		-keyout "$tls_directory/$name.key" -out "$tls_directory/$name.csr" \
		-subj "/CN=$name" \
		-addext "subjectAltName=DNS:$name,DNS:localhost,IP:127.0.0.1" >/dev/null 2>&1
	openssl x509 -req -in "$tls_directory/$name.csr" \
		-CA "$tls_directory/ca.crt" -CAkey "$tls_directory/ca.key" -CAcreateserial \
		-out "$tls_directory/$name.crt" -days 3650 -sha256 -copy_extensions copy >/dev/null 2>&1
	rm -f "$tls_directory/$name.csr"
}

issue_server_certificate webhook-receiver
issue_server_certificate adapter-runner
issue_server_certificate model-runner

# --- isolated runner credentials --------------------------------------------
if [ ! -f "$runner_directory/credential" ]; then
	printf 'idq_wrk_v1_%s\n' "$(generate_key)" > "$runner_directory/credential"
fi
if [ ! -f "$runner_directory/gateway_credential" ]; then
	printf 'idq_wrk_v1_%s\n' "$(generate_key)" > "$runner_directory/gateway_credential"
fi
if [ ! -f "$runner_directory/app_id" ]; then
	printf 'idenqa-self-hosted-dev-app\n' > "$runner_directory/app_id"
fi
if [ ! -f "$runner_directory/api_key" ]; then
	printf 'idenqa-self-hosted-dev-api-key\n' > "$runner_directory/api_key"
fi
if [ ! -f "$runner_directory/model_credential" ]; then
	printf 'idq_wrk_v1_%s\n' "$(generate_key)" > "$runner_directory/model_credential"
fi
if [ ! -f "$runner_directory/model_gateway_credential" ]; then
	printf 'idq_wrk_v1_%s\n' "$(generate_key)" > "$runner_directory/model_gateway_credential"
fi

# --- packaged adapter-runner fixture ----------------------------------------
adapter-runner-fixture \
	--output "$runtime_directory/adapter-runner.json" \
	--listen-address 0.0.0.0:9090 \
	--certificate-file "$tls_directory/adapter-runner.crt" \
	--private-key-file "$tls_directory/adapter-runner.key" \
	--credential-file "$runner_directory/credential" \
	--app-id-file "$runner_directory/app_id" \
	--api-key-file "$runner_directory/api_key" \
	--gateway-url https://api:8443 \
	--gateway-ca-file "$tls_directory/ca.crt" \
	--gateway-credential-file "$runner_directory/gateway_credential" >/dev/null

chmod 0644 "$tls_directory/ca.crt"
chmod 0600 "$tls_directory"/*.key "$runtime_directory"/*.json "$runner_directory"/*
chown -R 10001:10001 "$runtime_directory" "$state_directory" /var/lib/idenqa

touch "$runtime_directory/.provisioned"
echo "provisioned self-hosted development credentials and certificates"
