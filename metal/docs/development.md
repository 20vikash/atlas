# Metal development

Run all Go commands from `metal/`. The repository root is not a Go module.

## Normal checks

```sh
gofmt -w <changed-go-files>
make test
make vet
go test -race ./...
make openapi
make build
```

Run `gofmt` only on changed Go files. Regenerate OpenAPI after an API annotation or schema change.

## eBPF generation

The `traffic` package embeds a local eBPF object built from `internal/network/traffic/bpf/track_traffic.c`.

Build it before a direct Go command:

```sh
make bpf
```

Generation needs Clang 12 or newer, Linux UAPI headers, and libbpf development headers.

The program needs Linux 6.6 or newer for TCX. The traffic integration tests also need root. See [integration testing](testing.md).

## Package changes

Read the nearest `SPEC.md` before a structural change. Keep interfaces small and define them in the consuming package. Add package and exported declaration comments.

Use `cmd/metald` as the composition root. Keep virtual machine state and operations in `internal/vm`. Keep host integration in the storage, network, systemd, and Firecracker packages.

## Host checks

Unit tests do not exercise KVM, ZFS, systemd, iptables, or a real Firecracker process. Use [integration testing](testing.md) on a disposable Linux host for these checks.

Read [operations](operations.md) before you change recovery behavior.
