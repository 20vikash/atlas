package host

import (
	"context"
	"runtime"
	"testing"

	vmmigration "github.com/frappe/atlas/metal/internal/vm_migration"

	"github.com/frappe/atlas/metal/internal/network"
	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/vm"
)

type testHostDependencies struct {
	privilegedAddresses []string
	wireGuardPeers      []network.WireGuardPeer
	images              []vm.Image
	virtualMachines     []vm.Information
	wakeCount           int
}

func (dependencies *testHostDependencies) ApplyPrivilegedAddresses(_ context.Context, addresses []string) error {
	dependencies.privilegedAddresses = append([]string(nil), addresses...)
	return nil
}

func (dependencies *testHostDependencies) Apply(_ context.Context, peers []network.WireGuardPeer) error {
	dependencies.wireGuardPeers = append([]network.WireGuardPeer(nil), peers...)
	return nil
}

func (dependencies *testHostDependencies) SetImagePolicies(_ context.Context, images []vm.Image) error {
	dependencies.images = append([]vm.Image(nil), images...)
	return nil
}

type testUnicastTransport struct {
	enabled  bool
	disabled bool
}

func (transport *testUnicastTransport) Enable(context.Context) error {
	transport.enabled = true
	return nil
}

func (transport *testUnicastTransport) Disable(context.Context) error {
	transport.disabled = true
	return nil
}

func (dependencies *testHostDependencies) List(context.Context) ([]vm.Information, error) {
	return append([]vm.Information(nil), dependencies.virtualMachines...), nil
}

func (dependencies *testHostDependencies) Capacity(context.Context) (storage.Capacity, error) {
	return storage.Capacity{TotalMiB: 4096, AvailableMiB: 3072}, nil
}

func TestSynchronizeAppliesControllerStateAndReportsCapacity(t *testing.T) {
	dependencies := &testHostDependencies{
		virtualMachines: []vm.Information{{ID: "vm-00001", State: vm.StateRunning, CPUMillicores: 1500}},
	}
	service, err := NewService(Dependencies{
		Mesh: dependencies, WireGuard: dependencies, Images: dependencies,
		VirtualMachines: dependencies, Storage: dependencies,
		Wake: func() { dependencies.wakeCount++ },
	})
	if err != nil {
		t.Fatal(err)
	}

	desired := DesiredState{
		PrivilegedVirtualMachineAddresses: []string{"fdaa::2"},
		WireGuardPeers:                    []network.WireGuardPeer{{Node: "node-2"}},
		Images:                            []vm.Image{{Name: "ubuntu"}},
	}
	result, err := service.Synchronize(t.Context(), desired)
	if err != nil {
		t.Fatal(err)
	}

	if len(dependencies.privilegedAddresses) != 1 || len(dependencies.wireGuardPeers) != 1 || len(dependencies.images) != 1 {
		t.Fatalf("controller state was not applied: %+v", dependencies)
	}
	if dependencies.wakeCount != 1 {
		t.Fatalf("wake count = %d, want 1", dependencies.wakeCount)
	}
	capacity := result.Capacity
	if capacity.AvailableCPUMillicores != max(runtime.NumCPU()*1000-1500, 0) || capacity.TotalStorageMiB != 4096 || capacity.AvailableStorageMiB != 3072 {
		t.Fatalf("capacity = %+v", capacity)
	}
	if result.VirtualMachineStates["vm-00001"] != vm.StateRunning {
		t.Fatalf("virtual machine states = %+v", result.VirtualMachineStates)
	}
}

func TestCapacitySubtractsMigrationReservations(t *testing.T) {
	dependencies := &testHostDependencies{
		virtualMachines: []vm.Information{{ID: "vm-00001", State: vm.StateRunning, CPUMillicores: 1500}},
	}
	reservations := func(context.Context) ([]vmmigration.TargetReservation, error) {
		return []vmmigration.TargetReservation{{VirtualMachineID: "vm-00002", CPUMillicores: 2500, MemoryMiB: 1024, DiskMiB: 2048}}, nil
	}
	service, err := NewService(Dependencies{
		Mesh: dependencies, WireGuard: dependencies, Images: dependencies,
		VirtualMachines: dependencies, Storage: dependencies,
		MigrationReservations: reservations, Wake: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}

	capacity, err := service.Capacity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if capacity.AvailableCPUMillicores != max(runtime.NumCPU()*1000-1500-2500, 0) {
		t.Fatalf("available CPU = %d", capacity.AvailableCPUMillicores)
	}
	if capacity.AvailableStorageMiB != 3072-2048 {
		t.Fatalf("available storage = %d, want 1024", capacity.AvailableStorageMiB)
	}
	// Migration targets are not running VMs.
	if capacity.VirtualMachineCount != 1 {
		t.Fatalf("VM count = %d, want 1", capacity.VirtualMachineCount)
	}
}

func TestSynchronizeAllowsMeshToBeDisabled(t *testing.T) {
	dependencies := &testHostDependencies{}
	service, err := NewService(Dependencies{
		WireGuard: dependencies, Images: dependencies, VirtualMachines: dependencies, Storage: dependencies,
		Wake: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Synchronize(t.Context(), DesiredState{}); err != nil {
		t.Fatal(err)
	}
}

func TestSynchronizeEnablesUnicastTransportInUnicastMode(t *testing.T) {
	dependencies := &testHostDependencies{}
	unicast := &testUnicastTransport{}
	service, err := NewService(Dependencies{
		WireGuard: dependencies, Images: dependencies, VirtualMachines: dependencies, Storage: dependencies,
		Unicast: unicast, Wake: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Synchronize(t.Context(), DesiredState{UnicastEnabled: true}); err != nil {
		t.Fatal(err)
	}

	if !unicast.enabled || unicast.disabled {
		t.Fatalf("unicast transport = enabled %t disabled %t, want an enabled transport", unicast.enabled, unicast.disabled)
	}
}

func TestSynchronizeDisablesUnicastTransportInMulticastMode(t *testing.T) {
	dependencies := &testHostDependencies{}
	unicast := &testUnicastTransport{}
	service, err := NewService(Dependencies{
		WireGuard: dependencies, Images: dependencies, VirtualMachines: dependencies, Storage: dependencies,
		Unicast: unicast, Wake: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Synchronize(t.Context(), DesiredState{}); err != nil {
		t.Fatal(err)
	}

	if !unicast.disabled || unicast.enabled {
		t.Fatalf("unicast transport = enabled %t disabled %t, want a disabled transport", unicast.enabled, unicast.disabled)
	}
}
