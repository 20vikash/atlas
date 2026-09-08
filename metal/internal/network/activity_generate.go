package network

// The directive below regenerates the eBPF activity objects and their Go
// bindings for the amd64 and arm64 targets. The step needs Clang. See
// docs/development.md for the required version. Run `make generate-network`.
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -target amd64,arm64 -type wake_event activity bpf/activity.c -- -I bpf/headers -O2 -g -Wall
