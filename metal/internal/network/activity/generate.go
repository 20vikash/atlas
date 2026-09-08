package activity

// Run `make generate-network` to update the committed eBPF objects and Go
// bindings. It needs Clang 12 or newer, BPF CPU v3, and Linux 6.6 or newer.
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -target amd64,arm64 -type wake_event activity bpf/activity.c -- -I bpf/headers -mcpu=v3 -O2 -g -Wall
