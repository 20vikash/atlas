BPF_OBJECT := internal/network/traffic/track_traffic.o
BPF_SOURCE := internal/network/traffic/bpf/track_traffic.c
OUTPUT_DIRECTORY := dist
BINARY_NAME := metald
SPEC_DIRECTORY := internal/api

# The API specification generator. go run keeps it out of the global toolchain.
SWAG := go run github.com/swaggo/swag/cmd/swag@v1.16.4

# A tag if one exists, else the short commit, with -dirty on uncommitted work.
# Falls back to "dev" in a tree with no commits. Override: make build VERSION=1.2.3
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

GO_BUILD_FLAGS := -trimpath -buildvcs=false -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: bpf build clean openapi test vet version

bpf: $(BPF_OBJECT)

$(BPF_OBJECT): $(BPF_SOURCE)
	clang -target bpfel -O2 -g -Wall -c $(BPF_SOURCE) -o $@

test: bpf
	go test ./...

vet: bpf
	go vet ./...

build: openapi bpf
	mkdir -p $(OUTPUT_DIRECTORY)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -o $(OUTPUT_DIRECTORY)/$(BINARY_NAME)-linux-amd64 ./cmd/metald
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GO_BUILD_FLAGS) -o $(OUTPUT_DIRECTORY)/$(BINARY_NAME)-linux-arm64 ./cmd/metald

openapi:
	$(SWAG) init -g main.go --dir ./cmd/metald,./$(SPEC_DIRECTORY) --parseInternal --outputTypes json -o $(SPEC_DIRECTORY)

version:
	@echo $(VERSION)

clean:
	rm -rf $(OUTPUT_DIRECTORY) $(BPF_OBJECT)
