BPF_OBJECT := cli/atlas-wg-mesh.bpf.o
BPF_SOURCE := bpf/bpf.c
BPF_HEADERS := $(wildcard bpf/*.h)
KERNEL_MODULE_OBJECT := cli/atlas-neigh.ko
OUTPUT_DIRECTORY := dist
CLI_NAME := atlas-wg-mesh

# The kernel the release module is built for. Override with the header
# directory of the kernel version that the hosts run.
KDIR ?= /lib/modules/$(shell uname -r)/build

# A tag if one exists, else the short commit, with -dirty on uncommitted work.
# Falls back to "dev" in a tree with no commits. Override: make build VERSION=1.2.3
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_BUILD_FLAGS := -trimpath -buildvcs=false -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: bpf module module-release build clean version

bpf: $(BPF_OBJECT)

# Build and install the module on this host for development.
module:
	$(MAKE) -C kernel

# The release build compiles the module once, so a host needs no compiler and
# no kernel headers. The module must carry a BTF section, or the BPF object
# cannot resolve the kfunc extern. Ubuntu header packages ship no vmlinux
# file, so the build uses the kernel BTF at /sys/kernel/btf/vmlinux as the
# base when the header directory holds none.
module-release:
	@if [ ! -e "$(KDIR)/vmlinux" ] && [ -e /sys/kernel/btf/vmlinux ]; then cp /sys/kernel/btf/vmlinux "$(KDIR)/vmlinux" || true; fi
	$(MAKE) -C kernel KDIR=$(KDIR)
	cp kernel/atlas_neigh.ko $(KERNEL_MODULE_OBJECT)

# Debian keeps the asm headers in a multiarch directory. The bpfel target stops
# clang from searching it, so name it. The directory is absent on other layouts.
BPF_INCLUDE_FLAGS := -I/usr/include/$(shell uname -m)-linux-gnu

$(BPF_OBJECT): $(BPF_SOURCE) $(BPF_HEADERS)
	clang -Oz -g -Wall -Werror $(BPF_INCLUDE_FLAGS) -target bpfel -c $(BPF_SOURCE) -o $@

build: bpf module-release
	mkdir -p $(OUTPUT_DIRECTORY)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -C cli $(GO_BUILD_FLAGS) -o ../$(OUTPUT_DIRECTORY)/$(CLI_NAME)-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -C cli $(GO_BUILD_FLAGS) -o ../$(OUTPUT_DIRECTORY)/$(CLI_NAME)-linux-arm64 .

version:
	@echo $(VERSION)

clean:
	rm -rf $(OUTPUT_DIRECTORY) $(BPF_OBJECT) $(KERNEL_MODULE_OBJECT)
