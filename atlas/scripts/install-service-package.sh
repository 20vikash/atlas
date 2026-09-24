#!/usr/bin/env bash
# Install an Atlas service package. You can run this script again after a failure.

set -eu

: "${PACKAGE_NAME:?PACKAGE_NAME is required}"
: "${PACKAGE_SETUP_SCRIPT:?PACKAGE_SETUP_SCRIPT is required}"
: "${PACKAGE_DOWNLOAD_URL:?PACKAGE_DOWNLOAD_URL is required}"
: "${PACKAGE_SHA256:?PACKAGE_SHA256 is required}"

source_dir=/opt/atlas/src/$PACKAGE_NAME
hash_file=/opt/atlas/$PACKAGE_NAME-package-hash

if [ "$(id -u)" -ne 0 ]; then
	echo "install-service-package must run as root" >&2
	exit 1
fi

step() { echo "==> $*"; }


step "check the installed package"
if [ -f "$hash_file" ] && [ "$(cat "$hash_file")" = "$PACKAGE_SHA256" ]; then
	echo "    $PACKAGE_NAME already matches $PACKAGE_SHA256"
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
archive=$download_directory/$PACKAGE_NAME.tar
curl -fsSL "$PACKAGE_DOWNLOAD_URL" -o "$archive"


step "check the package"
package_hash=$(sha256sum "$archive" | cut -d' ' -f1)
if [ "$package_hash" != "$PACKAGE_SHA256" ]; then
	echo "the package at $PACKAGE_DOWNLOAD_URL has hash $package_hash, expected $PACKAGE_SHA256" >&2
	exit 1
fi

# Refuse a member that could write outside the package directory.
while IFS= read -r member; do
	case "$member" in
		"$PACKAGE_NAME"/*) ;;
		*)
			echo "the package holds a name outside $PACKAGE_NAME/" >&2
			exit 1
			;;
	esac

	relative_path=${member#"$PACKAGE_NAME/"}
	case "/$relative_path/" in
		*/../* | */./* | //)
			echo "the package holds an unsafe name: $member" >&2
			exit 1
			;;
	esac
done < <(tar -tf "$archive")


step "unpack the package"
tar -xf "$archive" -C "$download_directory" --no-same-owner --no-same-permissions
rm -rf "$source_dir"
install -d -m 0755 "$(dirname "$source_dir")"
mv "$download_directory/$PACKAGE_NAME" "$source_dir"


step "run the setup script"
chmod +x "$source_dir/$PACKAGE_SETUP_SCRIPT"
"$source_dir/$PACKAGE_SETUP_SCRIPT"


step "record the installed package"
printf '%s\n' "$PACKAGE_SHA256" > "$hash_file"
chmod 0644 "$hash_file"

echo "$PACKAGE_NAME is installed at $PACKAGE_SHA256"
