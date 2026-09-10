# Metal host layout

For Go code, follow the repository [Go anti-pattern rules](../../llm/go-code-review-guide.md).

[metal SPEC](../SPEC.md) · overview: [architecture.md](architecture.md)

Metal keeps virtual machine state on disk. After a restart, it reads the state from these paths. The controller supplies each virtual machine ID.

```text
/etc/systemd/system/
├── metal.service                   the metald daemon, one for each host
└── metal-vm@.service               template unit; metald starts metal-vm@<id>

/var/lib/metal/
├── metald.toml                     metald configuration; metald.base_dir is this dir
├── images/                         derived from metald.base_dir
│   └── ubuntu/                     one directory for each image reference
│       ├── manifest.json           immutable digests and architecture
│       ├── vmlinux                 guest kernel, hard-linked into the jail
│       ├── boot-args               kernel command line, optional
│       ├── last-used               last successful VM start
│       └── warm/<key>/              local warm artifacts for one exact shape
│           ├── state               Firecracker device state
│           └── memory              guest memory
├── snapshots/<id>/                 temporary Machine image staging
│   ├── metadata.json
│   └── vmlinux
├── image-policies.json             desired cached images from the controller
├── wireguard-peers.json            atomically saved managed peer set
└── machines/                       derived from metald.base_dir
    └── <id>/                       one directory for each VM ID
        ├── config.json             versioned reservation and desired state
        ├── status.json             versioned observed state and cleanup progress
        ├── jailer.env              JAILER_ARGS for metal-vm@<id>.service
        ├── saved-state/            one resumable state for automatic idle shutdown
        │   ├── state
        │   ├── memory
        │   └── metadata.json
        └── firecracker/            the executable name that jailer appends
            └── <id>/
                └── root/           the VM sees this as /
                    ├── firecracker jailer copies the exec file in
                    ├── vmlinux     hard link to the kernel
                    ├── rootfs.img  block node for the VM zvol
                    ├── saved-state-pending/  state before atomic publication
                    └── run/
                        └── firecracker.socket

/run/metal/                         firecracker.sockets_dir; tmpfs, remade on boot
└── <id>.sock                       link to the socket in the VM jail

/run/netns/
└── metal-<id>                      the VM network namespace
```

The host veth is `vh-<user-id>`. The namespace veth is `vg-<user-id>`. The TAP name is `tap0`.

Automatic idle shutdown uses this local cycle:

```text
idle timeout                     IP packet or configuration change
running -- save state --> stopped -- restore state --> running
                            |
                            +-> Firecracker: stopped, MainPID 0
                            +-> saved state and vms/<id>: kept
```

The saved state is under `machines/<id>/saved-state/`. Metal writes it under `saved-state-pending` and publishes it with one atomic rename.

The jail is inside the VM directory. Removing `machines/<id>` removes the VM and its chroot. Jailer adds `<exec>/<id>/root` below the VM directory, so the jail stays separate from the VM's other files. The jail base is not configurable: the kernel is hard-linked into the jail, and a hard link cannot cross a filesystem.

A Unix socket address holds 108 bytes. Metal uses a short link because the jail socket path can exceed this limit. The veth names use the VM user ID to meet the Linux interface name limit.

The ZFS pool holds the VM disks. These are dataset names, not directories:

```text
metal/images/ubuntu          base ZFS volume with a @ready snapshot
metal/images/ubuntu@ready    source for normal VM disks
metal/vms/<id>               one VM disk clone
metal/staging/<image-id>     temporary read-only Machine image upload source
metal/warm/<key>@ready       local warm disk for one image and exact VM shape
```

A VM disk keeps the VM ID. The paths `machines/<id>` and `vms/<id>` identify the same VM.

metald does not create the ZFS pool or select its device. Host setup creates the pool before metald starts, and `zfs.pool` names it.

What each path holds and why: [internal/storage/SPEC.md](../internal/storage/SPEC.md) for datasets and images, [internal/firecracker/SPEC.md](../internal/firecracker/SPEC.md) for the jail, and [internal/vm/SPEC.md](../internal/vm/SPEC.md) for the records.
