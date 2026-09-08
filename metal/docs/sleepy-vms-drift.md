# Sleepy VMs: decisions and drift

This records what the handoff plan (`docs/sleepy-vms-implementation.txt`) decided and
where the built branch diverged. It is a review aid for the pull request. It is not
committed and not the SPEC. The SPEC and `docs/*.md` carry the shipped behavior.

Phase 3 is complete and the end-to-end path is validated on a real host.

## 1. Required design decisions (1 to 22)

| # | Decision | Status | Note |
|---|---|---|---|
| 1 | Attach TCX to ingress and egress of tap0 | Drifted | Egress only. The ingress hook was dropped, because the guest's own frames kept a VM awake. |
| 2 | Require Linux 6.6+ TCX; clear startup error | Partial | Runtime needs 6.6+ and the atomic needs BPF cpu v3. No explicit startup feature probe was added; a load failure surfaces instead. |
| 3 | Count every Ethernet frame, do not parse | Drifted | Counts host-to-guest TCP over IPv4 or IPv6 only. Host evidence (IPv6 MLD, ARP) forced a filter. User decision. |
| 4 | Observe and signal only, always TCX_NEXT, never change a packet | Held | The program reads the ethertype and IP protocol but changes no packet and always returns TCX_NEXT. |
| 5 | One program instance serves both links | Drifted | One instance per VM on the egress link only. |
| 6 | metald owns maps and links, no bpffs pin, restart is a new baseline | Held | |
| 7 | cilium/ebpf and bpf2go, commit generated files, amd64 and arm64 | Held | Added `-mcpu=v3` and `-type wake_event` to the generate command. |
| 8 | `LastNetworkActivity` returns `LastSeenAt` and `HasBeenSeen` | Extended | Added `LastPacketMonotonicNanoseconds` for an exact abort check. The original two fields are unchanged. |
| 9 | eBPF stores `bpf_ktime_get_ns`; Go converts age to wall time | Held | The raw monotonic value is now also exposed for the abort, still never stored as wall time. |
| 10 | `is_sleepy` on Specification and the API; a real change raises the generation | Held | |
| 11 | `[sleep] enabled/idle_timeout`, one Metal-wide value, off by default | Held | |
| 12 | `sleeping` is observed only | Held | |
| 13 | Explicit Start and Stop modes | Held | `StartNormal`, `StartFromSleepSnapshot`, `StartFromSleepSnapshotPaused`, `StopShutdown`, `StopWithSleepSnapshot`. |
| 14 | Restart stays a guest restart | Held | |
| 15 | Store sleep artifacts below `machines/<id>/sleep/` | Drifted | Unified under `machines/<id>/snapshots/generations/<n>/`, shared with warm-image snapshots (Phase 2 refactor). |
| 16 | Full snapshot, pause first, keep file sync, no second disk snapshot | Held | |
| 17 | Publish atomically, manifest last, then terminate | Held | Path is the unified snapshots path, not `sleep/`. |
| 18 | New generation per sleep, never overwrite a mapped memory file | Held | |
| 19 | Manifest fields, validate all, derive paths from VM ID | Held | Manifest also carries the specification and restart generations used by the sleep gate. |
| 20 | A bad snapshot is a failure, no silent cold boot | Held, refined | A snapshot made stale by a specification-shape change cold boots on purpose, which extends the "explicit discard" cases. |
| 21 | The trigger packet can be lost, clients retry, test uses TCP | Held | |
| 22 | One VM operation lock owns idle, stop, state, and wake | Held | A wake event waits for the same lock. |

## 2. Structural drift

### Activity direction: egress only
The plan counts both directions. The build counts the tap0 egress hook only, which
carries host-to-guest traffic. Reason: the guest's own ingress frames (link-local
IPv6, ARP) kept an idle VM awake. Wake is unaffected, because a sleeping VM has no
guest to emit frames and an incoming packet hits the egress hook.

### Classification: TCP only (user decision)
The plan counts every frame and states that ARP and neighbour discovery can wake a VM
before the first IP packet. The build counts and wakes on host-to-guest TCP only.
This overrides decisions 3 and the parsing part of the first plan. It has one
important consequence, handled below: ARP can no longer wake a VM.

### Unify manual warm stop and automatic sleep on `sleeping` (user decision)
The plan's state table treated a manual warm stop and automatic sleep separately. The
build reaches one observed `sleeping` state for both. `reconcileSleeping` then holds,
resumes, or discards from that single state.

### Sleep artifact location
`machines/<id>/sleep/generations/<n>/` in the plan became
`machines/<id>/snapshots/generations/<n>/`, shared with warm-image snapshots through
one create-and-publish path.

## 3. New decisions that were not in the plan

These arose during Phase 3 and the host run. Each is a small design update, not a plan
step.

1. **TCP classifier in eBPF.** Reads the ethertype and the IPv4 or IPv6 protocol
   field. Only a TCP segment counts or wakes. (User decision, section 2.)
2. **BPF cpu v3 and a BTF anchor.** The 32-bit atomic compare-and-swap for wake
   deduplication needs `-mcpu=v3`. bpf2go needs an unused global to emit the wake
   event type. Both are in the generate command and the C source.
3. **Abort on the monotonic packet time, not wall time.** The sleep abort compared a
   wall-clock time derived from two separate clock reads, so nanosecond read jitter
   aborted a sleep with no traffic. This was a production bug: automatic sleep would
   rarely have stayed asleep. Fixed by comparing the raw eBPF monotonic value.
4. **Pin the guest neighbour entry.** A sleeping VM has no process to answer ARP, and
   an ARP frame cannot wake it under TCP-only. Without a static neighbour entry a wake
   packet could not resolve the guest MAC and never reached tap0. `ensureNamespaceBase`
   now pins the fixed guest IP and MAC. This closes the gap opened by decision 3's
   drift and is the fix that made a normal VM wakeable.
5. **Arm through the reconcile pass, not a separate startup step.** The plan (3E) wanted
   a dedicated startup pass that validates every sleeping record and arms it before
   readiness. The build arms in `holdSleeping`, which runs on the first reconcile pass
   after a restart, and rearms every pass. A short window before that pass can lose one
   packet, which the retry contract already allows.
6. **`NetworkWakeMonitor` is a required manager dependency when sleep is enabled.** The
   manager arms and disarms through it, so `NewManager` rejects an enabled policy
   without it.

## 4. Commit-structure drift

The plan asked for 5 to 7 commits per chunk with specific splits. The build kept small,
building, tested commits but merged closely related ones and folded work that the
existing code already satisfied.

| Chunk | Plan commits | Built commits | Drift |
|---|---|---|---|
| 3A | 6 | 6 | Reordered: a TCP-classifier commit was added first; "define events" and "reserve" merged, because an unused ring type is pruned from BTF. |
| 3B | 6 | 5 | Shared-map plumbing folded into the arm commit; "read events" and "slow consumer" merged into one worker commit. |
| 3C | 6 | 2 | The reconciler loop and the manager wake each landed as one commit with tests, per the repo rule to keep tests with code. |
| 3D | 7 | 4 | Serialize-with-warm-stop and publish-before-queued-wake are inherent in the one VM lock, so they became tests and notes, not new code. Rearm-after-failure is covered by the hold-pass rearm. |
| 3E | 5 | 1 | The daemon already cancels workers, waits, then closes the monitor, so only the reconciler construction and start were new. Startup readiness arming was not a separate step (section 3.5). |
| 3F | 5 | 2 plus 2 fixes | One script plus one docs commit, then two host-found fixes (boot keepalive, then the production neighbour pin that supersedes it). |

## 5. Validation and gates

Ran in the sandbox for every chunk: `go build ./...`, `go vet ./...`,
`go test ./...`, `go test -race ./...`, and the eBPF generate diff check.

- `make openapi` was not re-run in Phase 3, because no HTTP schema changed.
- `sudo test/integration/sleepy-vm-test.sh` **passed on the host**, both sleep and wake
  cycles, with guest memory continuity. This is the core Gate 2 and Gate 3 evidence.
- Still owed on a host: the privileged `go test -tags integration ./internal/network/`
  and `./internal/firecracker/` suites. They cannot run in the sandbox (no root, no KVM).
- Gate 3 was resolved inside Phase 3 rather than as a separate later change: the exact
  unwanted traffic (host-end IPv6 MLD, ARP) was identified and the TCP-only filter
  applied. The build filters in eBPF, not after the fact.

## 6. Not done or deferred

- **Console kills the VM on metald restart.** A pre-existing bug (regression from
  `b4f597e`, tty console). Deferred to a separate pull request. It affects production.
- **Startup readiness arming as a dedicated pass** (plan 3E commit 3). Replaced by the
  reconcile-pass rearm.
- **Barrier-controlled per-boundary wake tests** (plan 3D commit 6). The lock makes the
  boundaries safe by construction; unit tests cover the observable outcomes instead.
- **The end-to-end script still pins its own neighbour entry.** With the production pin
  in place this is redundant. Dropping it would make the script validate the production
  path. Left as a follow-up.

## 7. Behavior consequences worth a reviewer's attention

- A sleepy VM with a short idle timeout can sleep mid-boot, because early boot has no
  host-to-guest TCP. This is arguably correct (no traffic, so sleep), and a client
  retry wakes it. The test works around it by pinning the neighbour entry so its ssh
  probes egress during boot.
- ARP and neighbour discovery no longer wake a VM. Only TCP does. The pinned neighbour
  entry removes the ARP dependency for reaching a sleeping guest.
- An established TCP or vsock connection is not guaranteed to survive the warm VMM
  restart. This matches the plan's out-of-scope list.
