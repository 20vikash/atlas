package firecracker

import (
	"context"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/vm"
)

// RefreshMetadata replaces the current MMDS data for an active process.
func (runtime *Runtime) RefreshMetadata(ctx context.Context, input vm.RuntimeMachine) error {
	status, err := runtime.Inspect(ctx, input)
	if err != nil {
		return err
	}
	if status.State != vm.StateRunning && status.State != vm.StatePaused {
		return nil
	}
	return api.New(runtime.configuration.socketPath(input.ID)).PutMMDS(ctx, input.Specification.MetadataServiceData(
		input.ID,
		input.NetworkInterface.GuestIPAddress,
		input.NetworkInterface.MACAddress,
	))
}
