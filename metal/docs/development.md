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

## Package changes

Read the nearest `SPEC.md` before a structural change. Keep interfaces small and define them in the consuming package. Add package and exported declaration comments.

Use `cmd/metald` as the composition root. Keep virtual machine state and operations in `internal/vm`. Keep host integration in the storage, network, systemd, and Firecracker packages.

## Host checks

Unit tests do not exercise KVM, ZFS, systemd, iptables, or a real Firecracker process. Use [integration testing](testing.md) on a disposable Linux host for these checks.

Read [operations](operations.md) before you change recovery behavior.
