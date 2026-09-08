package network

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/frappe/atlas/metal/internal/network/activity"
)

// fakeActivityMonitor records the attach and release calls of the allocator.
type fakeActivityMonitor struct {
	ensured  []activity.AttachmentRequest
	released []string
}

func (attacher *fakeActivityMonitor) EnsureAttachment(request activity.AttachmentRequest) error {
	attacher.ensured = append(attacher.ensured, request)
	return nil
}

func (attacher *fakeActivityMonitor) ReleaseAttachment(virtualMachineID string) error {
	attacher.released = append(attacher.released, virtualMachineID)
	return nil
}

func TestNewLinuxAllocatorStoresTheActivityAttacher(t *testing.T) {
	attacher := &fakeActivityMonitor{}
	allocator := NewLinuxAllocator(nil, attacher)
	if allocator.activityMonitor != attacher {
		t.Error("the activity monitor was not stored")
	}
}

func TestGuestMACAddressIsTheSameForEveryVirtualMachine(t *testing.T) {
	allocator := &LinuxAllocator{}
	address := allocator.interfaceFor("vm-1").MACAddress
	if address != allocator.interfaceFor("vm-2").MACAddress {
		t.Error("MAC address differs between virtual machines")
	}
	if address != "06:00:ac:10:00:02" {
		t.Errorf("MAC address = %q", address)
	}
}

type fakeMesh struct {
	added     []string
	removed   []string
	removeErr error
}

func (mesh *fakeMesh) Add(_ context.Context, address, interfaceName string) error {
	mesh.added = append(mesh.added, address+" "+interfaceName)
	return nil
}

func (mesh *fakeMesh) Remove(_ context.Context, address, interfaceName string) error {
	mesh.removed = append(mesh.removed, address+" "+interfaceName)
	return mesh.removeErr
}

func TestRemoveMeshRegistrationNamesTheHostVirtualEthernet(t *testing.T) {
	mesh := &fakeMesh{}
	allocator := &LinuxAllocator{mesh: mesh}

	if err := allocator.removeMeshRegistration(context.Background(), 100000, "fdaa:1:0:1::1"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"fdaa:1:0:1::1 vh-100000"}; !slices.Equal(mesh.removed, want) {
		t.Errorf("removed = %v, want %v", mesh.removed, want)
	}
}

func TestMeshRegistrationSkipsAVirtualMachineWithoutAnAddress(t *testing.T) {
	mesh := &fakeMesh{}
	allocator := &LinuxAllocator{mesh: mesh}

	if err := allocator.removeMeshRegistration(context.Background(), 100000, ""); err != nil {
		t.Fatal(err)
	}
	if err := allocator.addMeshRegistration(context.Background(), "vm-1", 100000, ""); err != nil {
		t.Fatal(err)
	}
	if len(mesh.removed) != 0 || len(mesh.added) != 0 {
		t.Errorf("mesh calls = %v and %v, want none", mesh.added, mesh.removed)
	}
}

func TestRemoveMeshRegistrationWrapsTheRegistrarError(t *testing.T) {
	mesh := &fakeMesh{removeErr: errors.New("not registered")}
	allocator := &LinuxAllocator{mesh: mesh}

	err := allocator.removeMeshRegistration(context.Background(), 100000, "fdaa:1:0:1::1")
	if err == nil || !strings.Contains(err.Error(), "fdaa:1:0:1::1") {
		t.Errorf("error = %v, want the address", err)
	}
}
