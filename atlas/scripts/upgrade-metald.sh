#!/usr/bin/env bash
# Replace the metald binary on a provisioned host.

set -eu

: "${METALD_DOWNLOAD_URL:?METALD_DOWNLOAD_URL is required}"

installed_binary=${METALD_BINARY_PATH:-/usr/bin/metald}
previous_binary=$installed_binary.previous
service=metal.service

if [ "$(id -u)" -ne 0 ]; then
	echo "upgrade-metald must run as root" >&2
	exit 1
fi

if [ ! -x "$installed_binary" ]; then
	echo "$installed_binary is not installed; run install-metald.sh first" >&2
	exit 1
fi

step() { echo "==> $*"; }

# reported_version returns a tool version or "unknown".
reported_version() {
	local tool_version
	tool_version=$("$1" version 2>/dev/null | head -1) || tool_version=""
	[ -n "$tool_version" ] || tool_version=unknown
	echo "$tool_version"
}

# unit_property returns a systemd service property.
unit_property() {
	systemctl show "$service" -p "$1" --value 2>/dev/null || echo ""
}

# service_is_stable checks that the service stays active.
service_is_stable() {
	local checks_remaining=5

	while [ "$checks_remaining" -gt 0 ]; do
		systemctl is-active --quiet "$service" || return 1
		checks_remaining=$((checks_remaining - 1))
		[ "$checks_remaining" -eq 0 ] || sleep 1
	done

	return 0
}

staged_binary=$(mktemp "$installed_binary.staged.XXXXXX")
trap 'rm -f "$staged_binary"' EXIT

step "download metald"
curl -fsSL -o "$staged_binary" "$METALD_DOWNLOAD_URL"
chmod 0755 "$staged_binary"

# Run the new binary before it replaces the running one.
new_version=$(reported_version "$staged_binary")
if [ "$new_version" = unknown ]; then
	echo "the downloaded binary has no version command; it is not a metald build" >&2
	exit 1
fi

current_version=$(reported_version "$installed_binary")
step "upgrade metald from $current_version to $new_version"

# A restart keeps the console descriptors that systemd holds, so the virtual
# machines stay up. An empty store means that this metald never stored them.
stored_console_count=$(unit_property NFileDescriptorStore)
if [ "${stored_console_count:-0}" = "0" ]; then
	echo "    systemd holds no console descriptor; running virtual machines stop during the restart"
else
	echo "    systemd holds $stored_console_count console descriptors; the virtual machines stay up"
fi

# Keep the running binary before anything changes, so every later step can undo.
step "back up $current_version to $previous_binary"
cp -a "$installed_binary" "$previous_binary"

# Rename instead of overwrite. The running process keeps its own inode.
step "install $installed_binary"
if ! mv -f "$staged_binary" "$installed_binary"; then
	echo "could not install $installed_binary; the running binary is unchanged" >&2
	exit 1
fi

# Restart rather than stop and start. systemd keeps its descriptor store across a
# restart even when the unit does not set FileDescriptorStorePreserve=yes.
step "restart $service"
if ! systemctl restart "$service" || ! service_is_stable; then
	echo "    $service did not start; restoring $current_version" >&2
	mv -f "$previous_binary" "$installed_binary"
	if ! systemctl restart "$service" || ! service_is_stable; then
		echo "    $service did not start with $current_version" >&2
	fi
	exit 1
fi

step "running metald $(reported_version "$installed_binary")"
echo "    previous binary: $previous_binary"

if [ "$(unit_property FileDescriptorStorePreserve)" != "yes" ]; then
	echo "    warning: $service has no FileDescriptorStorePreserve=yes"
	echo "    a stop of $service still stops every virtual machine"
	echo "    run the Metal Server action Re-configure Metald to update the unit"
fi

step "virtual machines on this host"
systemctl list-units "metal-vm@*" --all --no-legend --no-pager | cat
