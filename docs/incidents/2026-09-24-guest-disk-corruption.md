# Guest disk corruption after a restart

| Field          | Value                                                              |
| -------------- | ------------------------------------------------------------------ |
| Date           | 2026-09-24                                                         |
| Environment    | Staging, region par-2                                              |
| Component      | Metal (`metal/internal/firecracker`)                               |
| Severity       | High: guest data loss                                              |
| Status         | Fixed                                                              |
| Affected up to | `4483743e67e84c3004c19437cc77799a01725f97` (2026-09-24, `develop`) |

## Summary

Five tenant VMs on node-par-2-00003 got ext4 corruption after a stop and start during the staging upgrade. Metal resumed a shared warm memory snapshot over the changed disk of each VM. Four VMs were terminated. One VM, vm-0000090, still runs with a damaged disk.

## Impact

| VM                                 | Result                                                                      |
| ---------------------------------- | --------------------------------------------------------------------------- |
| vm-0000083, vm-0000085, vm-0000089 | sshd did not start. Terminated.                                             |
| vm-0000088                         | ext4 reported "Structure needs cleaning". Terminated.                       |
| vm-0000090                         | sshd fails with `libkrb5.so.3: invalid ELF header`. Kept for data recovery. |
| vm-0000078                         | One damaged binary. The VM runs.                                            |

VMs with a shape that did not match their warm image (CPU, memory, and disk) were not affected.

## Detection

After the restart, SSH to some VMs failed or closed at once. A read-only `e2fsck` on a clone of the vm-0000090 disk stopped with "directory corrupted". The guest journal showed the first ext4 error about two minutes after the restart.

## Timeline (UTC)

| Time                     | Event                                                                                                  |
| ------------------------ | ------------------------------------------------------------------------------------------------------ |
| 2026-09-18 19:01         | The IMG-0039 warm image boots, and Metal captures its memory.                                          |
| 2026-09-22 09:36         | vm-0000090 is created. Metal clones the image disk and resumes the warm memory. Disk and memory match. |
| 2026-09-22 to 2026-09-24 | The VM runs for 2 days and writes to its disk.                                                         |
| 2026-09-24 09:27:54      | An operator stops and starts all VMs. Metal resumes the same warm memory over the changed disk.        |
| 2026-09-24 09:27:59      | The guest reads wrong file contents (`cmp: Exec format error`).                                        |
| 2026-09-24 09:30:00      | The guest kernel reports the first ext4 error (`deleted inode referenced`).                            |
| 2026-09-24 13:23         | sshd fails on each connection.                                                                         |

## Root cause

`machine.Start` resumed the shared warm image whenever the VM shape matched. `provisionDisk` clones the image disk only when the VM has no disk. For an existing VM, Metal resumed the memory of a guest that had booted on the original image disk over the VM's own disk. The guest kernel kept the inode tables, block bitmaps, and page cache of the image, and it wrote that state over the VM disk.

A guest keeps its warm boot ID after each resume. vm-0000090 had one boot ID from 2026-09-18 to 2026-09-24, so it never did a cold boot.

These paths called `Start` for an existing disk:

- A stop and a start.
- The reboot action (`applyRestart`).
- A wake after Metal deleted the saved state because of a specification or restart change.
- A start after a host or metald restart.

Sleep and wake use the saved memory of the VM itself, so they were not affected. Migration uses `ColdStart` or the transferred state, so it was not affected.

### Contributing factors

- `Stop` ends Firecracker without a guest shutdown, so the guest loses writes that are still in its memory.
- The IMG-0039 disk was captured while its source VM ran. Its first boot recovered 36 orphan inodes.

## Resolution

`machine.Start` checks `HasDisk` first. It resumes the warm image only on the first boot, before the VM has its own disk. Every later start uses a cold boot. The same change stops the warm failure path from releasing the disk of an existing VM.

## Lessons

- A memory snapshot is valid only with the exact disk state that it was captured with.
