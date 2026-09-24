# Metal architecture

Metal manages Firecracker virtual machines on one host. It stores host intent on disk and makes host resources match that intent.

Read [How Atlas works](../../docs/architecture.md) first if the Atlas and Metal ownership boundary is new to you.

## The 2 rules that shape Metal

### systemd owns each VM process

Metal asks systemd to start and stop each Firecracker process. A `metald` restart does not stop a running VM.

After a restart, Metal reads files, ZFS, and systemd. It does not depend on old process memory.

### Metal stores intent, not commands

Atlas sends the complete desired VM state. Metal stores that state before it returns `202 Accepted`.

Reconcilers compare desired state with observed state. They repeat safe operations until both states match.

## Component map

```mermaid
flowchart TB
    Atlas[Atlas HTTPS API]
    Peer[Metal node coordination API]
    API[internal/api]
    VM[vm.Manager]
    Host[host.Service]
    Reconcile[Reconcilers]
    Runtime[firecracker.Runtime]
    Storage[Storage stores]
    Network[Network manager]
    Traffic[Traffic monitor]
    Systemd[systemd]
    Jailer[jailer]
    Firecracker[Firecracker]

    Atlas --> API
    Peer --> API
    API --> VM
    API --> Host
    API --> Reconcile
    VM --> Runtime
    VM --> Storage
    VM --> Network
    Host --> Storage
    Host --> Network
    Reconcile --> VM
    Reconcile --> Storage
    Traffic --> VM
    Runtime --> Systemd
    Systemd --> Jailer
    Jailer --> Firecracker
```

`cmd/metald` creates these concrete services. Consumer packages define small interfaces only when they need a test or ownership boundary.

Read the [package map](../internal/SPEC.md) when you need the exact dependency direction.

## Request and reconciliation flow

An API handler validates a request and stores desired state. It then wakes a reconciler and returns before all host work finishes.

```mermaid
sequenceDiagram
    participant Atlas
    participant API as Metal API
    participant Record as VM record
    participant Reconciler
    participant Host as Host resources

    Atlas->>API: PUT desired VM state
    API->>API: Authenticate and validate
    API->>Record: Store desired state
    API->>Reconciler: Wake
    API-->>Atlas: 202 Accepted
    Reconciler->>Record: Read desired and observed state
    Reconciler->>Host: Apply one safe state change
    Host-->>Reconciler: Current result
    Reconciler->>Record: Store observed state or error
```

One pass makes bounded progress. A later pass continues after an error or restart.

## Durable state

```mermaid
flowchart LR
    Desired[config.json<br/>reservation and desired state]
    Observed[status.json<br/>observed state and cleanup]
    Process[systemd<br/>process state]
    Data[ZFS<br/>images, disks, and snapshots]

    Desired --> Reconcile[Reconciler]
    Observed --> Reconcile
    Process --> Reconcile
    Data --> Reconcile
    Reconcile --> Observed
```

Metal refuses to start when it cannot decode a VM record. A silent record loss is more dangerous than a stopped daemon.

The [host layout](host-layout.md) lists every persistent and runtime path.

## VM start scenario

```mermaid
flowchart TD
    Desired[Desired state says Running] --> Lock[Take the VM operation lock]
    Lock --> Disk[Prepare ZFS disk]
    Disk --> Network[Converge namespace and interfaces]
    Network --> Console[Open serial console PTY]
    Console --> Unit[Start systemd unit]
    Unit --> Socket[Wait for Firecracker API socket]
    Socket --> Boot{First boot with a valid warm artifact?}
    Boot -->|Yes| Warm[Load memory and device state]
    Boot -->|No| Cold[Configure and boot VM]
    Warm --> Observe[Store observed state]
    Cold --> Observe
```

Warm start is an optimization for the first boot only. Cold start is used for every later start, and when a warm artifact is absent or invalid.

## Failure and cleanup rules

- Metal holds one operation lock for each VM.
- Desired state remains durable after a runtime error.
- Cleanup progress remains on disk until every owned resource is gone.
- Mutual TLS protects Atlas API and node coordination requests.
- The source and destination keep migration locks until finish or rollback completes.

## Read next

| Topic | Guide |
| --- | --- |
| VM lifecycle and power state | [VM functionality](vm.md) |
| Images, disks, and snapshots | [Storage](storage.md) |
| Namespaces, routes, and WG Mesh | [Networking](networking.md) |
| Controller endpoints | [HTTP API](api.md) |
| Files and host resources | [Host layout](host-layout.md) |
| Development host setup | [Integration testing](testing.md) |
| Migration internals | [VM migration specification](../internal/vm/migration/SPEC.md) |
