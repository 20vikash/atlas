#!/usr/bin/env bash
# Install Pilot, set up Atlas, and build the guest images.
# Reads /root/atlas-vm.env, which atlas-deployer writes from atlas-deployer.toml.

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
# Pilot runs on whatever `python3` resolves to, and Atlas needs 3.14.
PYTHON_VERSION="${PYTHON_VERSION:-3.14}"
LETSENCRYPT_EMAIL="${LETSENCRYPT_EMAIL:-}"
ATLAS_REPOSITORY="${ATLAS_REPOSITORY:-https://github.com/frappe/atlas}"
ATLAS_BRANCH="${ATLAS_BRANCH:-develop}"
# One word for each image: version:architecture:variant (server or minimal).
IMAGES="${IMAGES:-24.04:amd64:server}"

PILOT_INSTALL_URL="https://raw.githubusercontent.com/frappe/pilot/$PILOT_BRANCH/install.sh"
PILOT_HOME="/home/$BENCH_USER/pilot"
BENCH_PATH="$PILOT_HOME/benches/$BENCH_NAME"
IMAGE_BUILDER_PATH="$BENCH_PATH/apps/atlas/atlas/vm/scripts/build_ubuntu_server_image.sh"
SETUP_GRANT="/etc/sudoers.d/$BENCH_USER-atlas-setup"
IMAGE_BUILDER_GRANT="/etc/sudoers.d/$BENCH_USER-atlas-image-builder"

force=false

say() { echo "==> $*"; }
fail() { echo "error: $*" >&2; exit 1; }
as_bench() { su - "$BENCH_USER" -c "$1"; }
quoted_password() { printf %q "$PASSWORD"; }

# `su -` sets no user bus, so `systemctl --user` needs both addresses.
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

# Pilot calls sudo with no terminal. The grant lives only as long as this run.
grant_setup_sudo() {
	install_grant "$SETUP_GRANT" "$BENCH_USER ALL=(ALL) NOPASSWD: ALL"
	trap 'rm -f "$SETUP_GRANT"' EXIT
}

# The units outlive $PILOT_HOME, so stop and remove them before the directory.
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

# Ubuntu 24.04 ships Python 3.12, which cannot parse the Atlas sources. Keep the
# system python3 for apt and give the bench user its own interpreter.
say "stage 4: python $PYTHON_VERSION for $BENCH_USER"
as_bench "command -v uv >/dev/null || curl -LsSf https://astral.sh/uv/install.sh | sh"
as_bench "uv python install $PYTHON_VERSION"
as_bench "mkdir -p ~/.local/bin && ln -sf \"\$(uv python find $PYTHON_VERSION)\" ~/.local/bin/python3"
reported_version=$(as_bench "python3 --version")
[[ $reported_version == *"$PYTHON_VERSION"* ]] ||
	fail "$BENCH_USER runs $reported_version; Pilot needs Python $PYTHON_VERSION to read the Atlas sources"
say "$BENCH_USER runs $reported_version"

say "stage 5: Pilot from branch $PILOT_BRANCH"
as_bench "PILOT_DEV=1 PILOT_BRANCH=$PILOT_BRANCH bash -c 'curl -fsSL $PILOT_INSTALL_URL | bash'"

say "stage 6: bench $BENCH_NAME"
# Guard each step on what it produces, so a stopped run continues here.
if [[ ! -f $BENCH_PATH/bench.toml ]]; then
	as_bench "pilot new $BENCH_NAME --admin-password $(quoted_password) --admin-domain $ADMIN_DOMAIN --database mariadb"
fi
if [[ ! -x $BENCH_PATH/env/bin/python ]]; then
	as_bench "pilot init --bench $BENCH_NAME"
fi

say "stage 7: site $SITE_NAME"
if [[ ! -f $BENCH_PATH/sites/$SITE_NAME/site_config.json ]]; then
	as_bench "pilot new-site $SITE_NAME --admin-password $(quoted_password) --bench $BENCH_NAME"
fi

# Pilot skips an app that is already there, so both stages are safe to repeat.
say "stage 8: the atlas app from $ATLAS_BRANCH"
as_bench "pilot get-app $ATLAS_REPOSITORY --branch $ATLAS_BRANCH --bench $BENCH_NAME"

say "stage 9: production"
production_arguments="--admin-domain $ADMIN_DOMAIN"
production_arguments+="${LETSENCRYPT_EMAIL:+ --tls --letsencrypt-email $LETSENCRYPT_EMAIL}"
as_bench "pilot setup production --bench $BENCH_NAME $production_arguments"

# Ensure nginx starts after reboot. Production setup does not enable it.
systemctl enable nginx
systemctl reload nginx
as_bench "pilot build --force --bench $BENCH_NAME"

# Production runs Redis and the workers, which an install hook can reach.
say "stage 10: install atlas on $SITE_NAME"
as_bench "pilot install-app $SITE_NAME atlas --bench $BENCH_NAME"

# The image builder runs its root file system build through sudo on every build.
say "stage 11: sudo grant for the image builder"
install_grant "$IMAGE_BUILDER_GRANT" "$BENCH_USER ALL=(ALL) NOPASSWD: $IMAGE_BUILDER_PATH *"

# Bootstrap has no object storage credentials yet, so every image is a site file.
say "stage 12: guest images ($IMAGES)"
for image in $IMAGES; do
	IFS=: read -r image_version image_architecture image_variant <<< "$image"
	image_arguments="--version $image_version --architecture $image_architecture --storage site-file"
	[[ $image_variant == minimal ]] && image_arguments+=" --minimal"
	say "image $image_version $image_architecture $image_variant"
	as_bench "pilot --bench $BENCH_NAME --site $SITE_NAME build-ubuntu-base-image $image_arguments"
done

say "ATLAS_SITE_READY $SITE_NAME"
