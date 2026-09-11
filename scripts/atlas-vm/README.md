# Atlas Pilot VM

`atlas-deployer` runs one Atlas bench in a Firecracker VM on a bare metal host. The host needs root, KVM, and a Secure Shell key pair.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/frappe/atlas/develop/scripts/atlas-vm/atlas_deployer.py |
	sudo install -m 0755 /dev/stdin /usr/local/bin/atlas-deployer
```

## Create the VM

```sh
curl -fsSLo atlas-deployer.toml https://raw.githubusercontent.com/frappe/atlas/develop/scripts/atlas-vm/atlas-deployer.example.toml
# Set pilot.site and pilot.password. Add one [[image]] table for each guest image.
sudo atlas-deployer create
```

## Use the VM

```sh
sudo atlas-deployer status
sudo atlas-deployer ssh
sudo atlas-deployer logs --setup --follow
sudo atlas-deployer restart
sudo atlas-deployer setup                        # run setup.sh again
sudo atlas-deployer resize --vcpu 8 --disk 60    # reboots the VM; a disk can only grow
```

The VM answers on port 2222, and host ports 80 and 443 reach it.

## Delete the VM

```sh
sudo atlas-deployer destroy
```

This deletes the bench, every site, and every database in the VM. There is no backup.
