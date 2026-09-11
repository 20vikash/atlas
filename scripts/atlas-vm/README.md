# Atlas Pilot VM

`atlas-deployer` runs one Atlas bench in a Firecracker VM on a bare metal host. Use it to get a working Atlas control plane on a fresh machine. The host runs one VM, because one Atlas controls the cluster.

## Files

| File | Purpose |
|---|---|
| `atlas_deployer.py` | The CLI. `create` installs it as `/usr/local/bin/atlas-deployer`. |
| `atlas-deployer.example.toml` | The configuration to copy and change. |
| `setup.sh` | Runs in the guest. Installs Pilot, the bench, the site, Atlas, and the guest images. |

## Requirements

The host needs root, KVM, an IPv4 default route, a Secure Shell key pair, and these commands: `curl`, `ip`, `iptables`, `unsquashfs`, `mkfs.ext4`, `truncate`, `ssh`, `scp`, `ssh-keygen`. The VM trusts the key it finds in `/root/.ssh` or in the home directory of the user behind `sudo`.

## Create the VM

```sh
cp atlas-deployer.example.toml atlas-deployer.toml
# Set pilot.site and pilot.password.
sudo ./atlas_deployer.py create
```

`create` downloads the Ubuntu 24.04 cloud image and the guest kernel, builds the guest disk, starts `atlas-pilot-vm.service`, waits for Secure Shell, and runs `setup.sh` in the VM.

## Commands

```text
create   build the VM, start it, and run setup.sh
start    stop     restart
status   report the VM, its size, and its ports
ssh      open a shell or run one command in the VM
logs     read the console log or the setup log
setup    run setup.sh again in a running VM
resize   change the vCPU count, the memory, or the disk size
destroy  stop the VM and delete every file it owns
```

Add `-v` before a command to print the guest console while the VM boots. A failed boot prints the last console lines on its own.

## Configuration

Edit `atlas-deployer.toml`. `[vm]` holds the machine size and the Secure Shell port. `[pilot]` holds the site, the password, and the bench user that `setup.sh` needs. `[atlas]` holds the repository and the branch. Each `[[image]]` table adds one guest image, for example Ubuntu 24.04 server and Ubuntu 24.04 minimal.

The addresses, the forwarded ports, the VM name, the bench name, and the Firecracker version are fixed in `atlas_deployer.py`.

`create` writes the resolved values to `/var/lib/atlas-vm/deployer.toml`, and every later command reads that state. It also renders `[pilot]`, `[atlas]`, and `[[image]]` into `/root/atlas-vm.env` in the guest, which `setup.sh` reads.

## Host layout

```text
/var/lib/atlas-vm/
    deployer.toml           the state every command reads
    bin/firecracker
    downloads/              cloud image and guest kernel
    atlas/
        rootfs.ext4         the guest disk
        firecracker.json    the machine the unit boots
        network             creates the tap device and the firewall rules
        console.log         setup.log
```

The unit `atlas-pilot-vm.service` owns the VM. Its `ExecStartPre` and `ExecStopPost` run the `network` script, so the tap device and the rules have one owner.

## Networking

The VM answers on `172.16.100.2` through the tap device `tap-atlas` on `172.16.100.1`. The host forwards its port 2222 to guest port 22, and ports 80 and 443 to the same guest ports. A forwarded port answers on the host address and on `127.0.0.1`.

## Change setup.sh

The guest downloads `setup.sh` from the Atlas repository, so a local change reaches a VM only after you push it. Use the local file while you work:

```sh
sudo atlas-deployer setup --script ./setup.sh
```

## Resize

```sh
sudo atlas-deployer resize --vcpu 8 --memory 16384 --disk 60
```

Firecracker fixes the machine size at boot, so `resize` stops the VM, changes the machine, starts it again, and grows the guest file system with `resize2fs`. A disk can only grow.

## Delete the VM

```sh
sudo atlas-deployer destroy
```

This deletes the guest disk with the bench, every site, and every database in it. There is no backup. It also deletes the cloud image and the kernel, unless you pass `--keep-downloads`.
