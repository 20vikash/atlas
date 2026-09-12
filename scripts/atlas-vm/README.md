# Atlas VM

`atlas-vm` runs one Atlas bench in a Firecracker VM on a bare metal host. The host needs root, KVM, and a Secure Shell key pair.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/frappe/atlas/develop/scripts/atlas-vm/atlas_vm.py |
	sudo install -m 0755 /dev/stdin /usr/local/bin/atlas-vm
```

## Create the VM

```sh
curl -fsSLo atlas-vm.toml https://raw.githubusercontent.com/frappe/atlas/develop/scripts/atlas-vm/atlas-vm.example.toml
# Set each value that contains change-me. Set the site names and email addresses.
sudo atlas-vm create
```

The configuration must contain the Atlas region, Scaleway, Route53, and Let's Encrypt values. Route53 must contain an existing public zone for `atlas.wildcard_domain`. Do not add `*.` to the domain.

The setup creates one Secure Shell key for the `pilot.user`. Atlas uses this key to manage Metal Servers. The setup keeps this key when you run it again.

The setup creates the Scaleway network resources and Route53 records. It also gets the provider catalogs and a wildcard certificate. Each `[[image]]` table creates one system image in site-file storage. Cargo configures object storage later.

The configuration contains passwords and provider keys. `atlas-vm` stores its copy at `/var/lib/atlas-vm/atlas-vm.toml` with mode `0600`.

## Use the VM

```sh
sudo atlas-vm status
sudo atlas-vm ssh
sudo atlas-vm logs --setup --follow
sudo atlas-vm restart
sudo atlas-vm setup                         # Apply the configuration again.
sudo atlas-vm reset-password pilot          # Use site for the site password.
sudo atlas-vm resize --vcpu 8 --disk 60     # This restarts the VM. A disk can only grow.
```

`atlas-vm setup` updates credentials and other mutable values. It stops if the configuration changes a region or provider value after provider setup.

The VM answers on port 2222, and host ports 80 and 443 reach it.

## Delete the VM

```sh
sudo atlas-vm destroy
```

This deletes the bench, every site, and every database in the VM. There is no backup.
