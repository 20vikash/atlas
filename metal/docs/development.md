# Metal development

Run all Go commands from `metal/`. The repository root is not a Go module.

## Normal checks

```sh
gofmt -w <changed-go-files>
go test ./...
go vet ./...
go test -race ./...
make openapi
make build
```

Run `gofmt` only on changed Go files. Regenerate OpenAPI after an API annotation or schema change.

## eBPF generation

The network package embeds compiled eBPF objects. The committed objects and Go bindings match the C source under `internal/network/bpf/`. A normal build does not run Clang, because the objects are committed.

Regenerate after a change to the C source, then confirm the tree is clean:

```sh
make generate-network
git diff --exit-code -- internal/network
```

Generation needs Clang 12 or newer. The vendored headers under `internal/network/bpf/headers/` remove the dependency on host kernel headers.

Running the programs needs a host kernel with TCX support, Linux 6.6 or newer. The privileged activity tests need root. See [integration testing](testing.md).

## Package changes

Read the nearest `SPEC.md` before a structural change. Keep interfaces small and define them in the consuming package. Add package and exported declaration comments.

Use `cmd/metald` as the composition root. Keep virtual machine state and operations in `internal/vm`. Keep host integration in the storage, network, systemd, and Firecracker packages.

## Host checks

Unit tests do not exercise KVM, ZFS, systemd, iptables, or a real Firecracker process. Use [integration testing](testing.md) on a disposable Linux host for these checks.

Read [operations](operations.md) before you change recovery behavior.
