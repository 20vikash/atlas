#!/usr/bin/env bash
# Install the Atlas HTTP proxy. You can run this script again after a failure.

set -eu

: "${HTTP_PROXY_DOWNLOAD_URL:?HTTP_PROXY_DOWNLOAD_URL is required}"
: "${HTTP_PROXY_PACKAGE_SHA256:?HTTP_PROXY_PACKAGE_SHA256 is required}"

source_dir=/opt/atlas/src/http-proxy
hash_file=/opt/atlas/http-proxy-package-hash

if [ "$(id -u)" -ne 0 ]; then
	echo "install-http-proxy must run as root" >&2
	exit 1
fi

step() { echo "==> $*"; }


step "check the installed package"
if [ -f "$hash_file" ] && [ "$(cat "$hash_file")" = "$HTTP_PROXY_PACKAGE_SHA256" ]; then
	echo "    the HTTP proxy already matches $HTTP_PROXY_PACKAGE_SHA256"
	exit 0
fi


step "download the package"
if ! command -v curl >/dev/null || ! command -v tar >/dev/null; then
	export DEBIAN_FRONTEND=noninteractive
	apt-get update -qq
	apt-get install -y -qq curl tar
fi

download_directory=$(mktemp -d)
trap 'rm -rf "$download_directory"' EXIT
archive=$download_directory/http-proxy.tar
curl -fsSL "$HTTP_PROXY_DOWNLOAD_URL" -o "$archive"


step "check the package"
package_hash=$(sha256sum "$archive" | cut -d' ' -f1)
if [ "$package_hash" != "$HTTP_PROXY_PACKAGE_SHA256" ]; then
	echo "the package at $HTTP_PROXY_DOWNLOAD_URL has hash $package_hash, expected $HTTP_PROXY_PACKAGE_SHA256" >&2
	exit 1
fi

# Refuse a member that could write outside the unpack directory.
if tar -tf "$archive" | grep -qvE '^http-proxy/[^/]'; then
	echo "the package holds a name outside http-proxy/" >&2
	exit 1
fi


step "unpack the package"
tar -xf "$archive" -C "$download_directory" --no-same-owner --no-same-permissions
rm -rf "$source_dir"
install -d -m 0755 "$(dirname "$source_dir")"
mv "$download_directory/http-proxy" "$source_dir"


step "run the proxy setup script"
chmod +x "$source_dir/nginx/setup.sh"
"$source_dir/nginx/setup.sh"


step "record the installed package"
printf '%s\n' "$HTTP_PROXY_PACKAGE_SHA256" > "$hash_file"
chmod 0644 "$hash_file"

echo "the HTTP proxy is installed at $HTTP_PROXY_PACKAGE_SHA256"
