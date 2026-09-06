package storage

import "testing"

func TestZFSPoolBuildsDatasetNames(t *testing.T) {
	pool := NewStores(t.Context(), "metal", "/images", nil).Pool
	if got := pool.baseDataset("ubuntu"); got != "metal/images/ubuntu" {
		t.Errorf("baseDataset = %q", got)
	}
	if got := pool.baseSnapshot("ubuntu"); got != "metal/images/ubuntu@ready" {
		t.Errorf("baseSnapshot = %q", got)
	}
	if got := pool.virtualMachineDataset("abc"); got != "metal/vms/abc" {
		t.Errorf("virtualMachineDataset = %q", got)
	}
	if got := pool.virtualMachineDevicePath("abc"); got != "/dev/zvol/metal/vms/abc" {
		t.Errorf("virtualMachineDevicePath = %q", got)
	}
}
