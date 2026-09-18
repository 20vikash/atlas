#!/usr/bin/env bash

set -eu

user_name=$(id -un)
user_home=$(getent passwd "$user_name" | cut -d: -f6)

if [ -z "$user_home" ] || [ ! -f "$user_home/.ssh/authorized_keys" ]; then
	echo "the current user has no authorized keys" >&2
	exit 1
fi

sudo -n install -d -m 700 -o root -g root /root/.ssh
sudo -n cp "$user_home/.ssh/authorized_keys" /root/.ssh/authorized_keys
sudo -n chown root:root /root/.ssh/authorized_keys
sudo -n chmod 600 /root/.ssh/authorized_keys
sudo -n nohup sh -c 'sleep 2; userdel --remove "$1"' sh "$user_name" >/dev/null 2>&1 &
