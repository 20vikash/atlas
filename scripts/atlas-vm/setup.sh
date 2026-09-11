#!/usr/bin/env bash
# Install Pilot, set up Atlas, and build the first guest image.

set -euo pipefail

environment_file="${ATLAS_VM_ENV:-/root/atlas-vm.env}"
# shellcheck source=/dev/null
[[ -f $environment_file ]] && source "$environment_file"

BENCH_NAME="${BENCH_NAME:-atlas}"
SITE_NAME="${SITE_NAME:?set SITE_NAME in the environment file}"
ADMIN_DOMAIN="${ADMIN_DOMAIN:-admin.$SITE_NAME}"
PASSWORD="${PASSWORD:?set PASSWORD in the environment file}"
BENCH_USER="${BENCH_USER:-frappe}"
PILOT_BRANCH="${PILOT_BRANCH:-develop}"
LETSENCRYPT_EMAIL="${LETSENCRYPT_EMAIL:-}"
ATLAS_REPOSITORY="${ATLAS_REPOSITORY:-https://github.com/frappe/atlas}"
ATLAS_BRANCH="${ATLAS_BRANCH:-develop}"
UBUNTU_VERSION="${UBUNTU_VERSION:-24.04}"
IMAGE_ARCHITECTURE="${IMAGE_ARCHITECTURE:-amd64}"
IMAGE_STORAGE="${IMAGE_STORAGE:-site-file}"

PILOT_INSTALL_URL="https://raw.githubusercontent.com/frappe/pilot/$PILOT_BRANCH/install.sh"
PILOT_HOME="/home/$BENCH_USER/pilot"
BENCH_PATH="$PILOT_HOME/benches/$BENCH_NAME"
IMAGE_BUILDER_PATH="$BENCH_PATH/apps/atlas/atlas/vm/scripts/build_ubuntu_server_image.sh"
SETUP_GRANT="/etc/sudoers.d/$BENCH_USER-atlas-setup"
IMAGE_BUILDER_GRANT="/etc/sudoers.d/$BENCH_USER-atlas-image-builder"

force=false

say() { echo "[$(date -Is)] == $* =="; }
fail() { echo "$*" >&2; exit 1; }
as_bench() { su - "$BENCH_USER" -c "$1"; }

# Pilot runs the database, Redis, and the workload as user units. `su -` sets no
# user bus, so `systemctl --user` needs both addresses.
as_bench_systemctl() {
	local uid
	uid=$(id -u "$BENCH_USER")
	su - "$BENCH_USER" -c "XDG_RUNTIME_DIR=/run/user/$uid \
		DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$uid/bus \
		systemctl --user $1" 2>/dev/null || true
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--force) force=true; shift ;;
		*) fail "unknown argument: $1" ;;
	esac
done

[[ $EUID -eq 0 ]] || fail "run this as root"

install_grant() {
	local path=$1 content=$2 staged
	staged=$(mktemp)
	echo "$content" > "$staged"
	visudo -cf "$staged" >/dev/null || fail "the sudo grant for $path is malformed"
	install -m 440 "$staged" "$path"
	rm -f "$staged"
}

# Pilot needs root for packages, nginx, certbot, and systemd, and it calls sudo
# with no terminal to answer a password prompt. The grant lives only as long as
# this run.
grant_setup_sudo() {
	install_grant "$SETUP_GRANT" "$BENCH_USER ALL=(ALL) NOPASSWD: ALL"
	trap 'rm -f "$SETUP_GRANT"' EXIT
}

# The database and its socket live under $PILOT_HOME, and the unit files do not.
# Deleting the directory while the daemon runs leaves a server that answers
# nothing and a unit that claims the bench is provisioned, so stop and remove
# the units first.
remove_pilot_installation() {
	echo "This deletes $PILOT_HOME with every bench, site, and database in it."
	echo "Certificates in /etc/letsencrypt stay, so a new setup reuses them."
	read -r -p "Type y to continue: " answer
	[[ $answer == y ]] || fail "stopped"

	if id "$BENCH_USER" >/dev/null 2>&1; then
		local unit_directory="/home/$BENCH_USER/.config/systemd/user"
		as_bench_systemctl "stop '$BENCH_NAME.target' '$BENCH_NAME-*' 'pilot-*'"
		as_bench_systemctl "disable '$BENCH_NAME.target' '$BENCH_NAME-*' 'pilot-*'"
		rm -f "$unit_directory"/"$BENCH_NAME"* "$unit_directory"/pilot-*
		rm -f "$unit_directory"/default.target.wants/"$BENCH_NAME"* \
			"$unit_directory"/default.target.wants/pilot-*
		as_bench_systemctl "daemon-reload"
		as_bench_systemctl "reset-failed"
	fi

	rm -rf "$PILOT_HOME"
	rm -f "$SETUP_GRANT" "$IMAGE_BUILDER_GRANT" "/etc/sudoers.d/$BENCH_USER-pilot-"*
}

if $force; then
	remove_pilot_installation
fi

say "stage 1: packages"
export DEBIAN_FRONTEND=noninteractive

# Build metald, Atlas WG Mesh, and the guest image.
apt-get update -qq
apt-get install -y -qq \
	wget curl git \
	make clang libbpf-dev linux-libc-dev \
	squashfs-tools zstd e2fsprogs

say "stage 2: host dependencies and the $BENCH_USER user"
wget -qO /root/pilot-install.sh "$PILOT_INSTALL_URL"
sh /root/pilot-install.sh --user "$BENCH_USER"

say "stage 3: sudo for $BENCH_USER during this run"
grant_setup_sudo

say "stage 4: Pilot from branch $PILOT_BRANCH"
as_bench "PILOT_DEV=1 PILOT_BRANCH=$PILOT_BRANCH bash -c 'curl -fsSL $PILOT_INSTALL_URL | bash'"

say "stage 5: bench $BENCH_NAME"
# `pilot new` writes bench.toml, and `pilot init` builds the environment. Guard
# each on what it produces, so a run that stopped inside init continues here.
if [[ ! -f $BENCH_PATH/bench.toml ]]; then
	as_bench "pilot new $BENCH_NAME --admin-password '$PASSWORD' --admin-domain $ADMIN_DOMAIN --database mariadb"
fi
if [[ ! -x $BENCH_PATH/env/bin/python ]]; then
	as_bench "pilot init --bench $BENCH_NAME"
fi

say "stage 6: site $SITE_NAME"
if [[ ! -f $BENCH_PATH/sites/$SITE_NAME/site_config.json ]]; then
	as_bench "pilot new-site $SITE_NAME --admin-password '$PASSWORD' --bench $BENCH_NAME"
fi

# Pilot skips an app that is already there, so both stages are safe to repeat.
say "stage 7: the atlas app from $ATLAS_BRANCH"
as_bench "pilot get-app $ATLAS_REPOSITORY --branch $ATLAS_BRANCH --bench $BENCH_NAME"

say "stage 8: production"
production_arguments="--admin-domain $ADMIN_DOMAIN"
production_arguments+="${LETSENCRYPT_EMAIL:+ --tls --letsencrypt-email $LETSENCRYPT_EMAIL}"
as_bench "pilot setup production --bench $BENCH_NAME $production_arguments"

# Ensure nginx starts after reboot. Production setup does not enable it.
systemctl enable nginx
systemctl reload nginx
as_bench "pilot build --force --bench $BENCH_NAME"

# Production runs Redis and the workers, which an install hook can reach.
say "stage 9: install atlas on $SITE_NAME"
as_bench "pilot install-app $SITE_NAME atlas --bench $BENCH_NAME"

# The image builder runs its root file system build through sudo on every build.
say "stage 10: sudo grant for the image builder"
install_grant "$IMAGE_BUILDER_GRANT" "$BENCH_USER ALL=(ALL) NOPASSWD: $IMAGE_BUILDER_PATH *"

say "stage 11: Ubuntu $UBUNTU_VERSION guest image"
image_arguments="--version $UBUNTU_VERSION --architecture $IMAGE_ARCHITECTURE"
image_arguments+="${IMAGE_STORAGE:+ --storage $IMAGE_STORAGE}"
as_bench "pilot --bench $BENCH_NAME --site $SITE_NAME build-ubuntu-base-image $image_arguments"

say "ATLAS_SITE_READY $SITE_NAME"
