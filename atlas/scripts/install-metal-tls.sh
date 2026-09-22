#!/usr/bin/env bash
# Install the regional CA and this host's Metal certificate.

set -eu

: "${METAL_TLS_CA_CERTIFICATE:?METAL_TLS_CA_CERTIFICATE is required}"
: "${METAL_TLS_CERTIFICATE:?METAL_TLS_CERTIFICATE is required}"
: "${METAL_TLS_PRIVATE_KEY:?METAL_TLS_PRIVATE_KEY is required}"

tls_directory=/var/lib/metal/tls
service=metal.service

if [ "$(id -u)" -ne 0 ]; then
	echo "install-metal-tls must run as root" >&2
	exit 1
fi

install -d -m 0700 "$tls_directory"
printf '%s' "$METAL_TLS_CA_CERTIFICATE" > "$tls_directory/ca.crt"
printf '%s' "$METAL_TLS_CERTIFICATE" > "$tls_directory/node.crt"
(umask 077 && printf '%s' "$METAL_TLS_PRIVATE_KEY" > "$tls_directory/node.key")
chmod 0600 "$tls_directory/ca.crt" "$tls_directory/node.crt" "$tls_directory/node.key"

# Metal reads these files once at startup. A first install runs before the unit exists.
if systemctl is-active --quiet "$service"; then
	echo "==> restart $service"
	if ! systemctl restart "$service"; then
		echo "$service did not start with the new certificate" >&2
		exit 1
	fi
fi
