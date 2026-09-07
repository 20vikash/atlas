package vm

import (
	"context"
	"fmt"
)

// sampleNetworkActivity reads the last packet time of one VM. The caller holds
// the VM operation lock. A read failure is wrapped with VM context and stays
// retryable. A missing attachment keeps vm.ErrNotFound for the caller to detect.
func (manager *Manager) sampleNetworkActivity(ctx context.Context, desired DesiredRecord) (NetworkActivity, error) {
	request := NetworkActivityRequest{VirtualMachineID: desired.ID, UserID: desired.UserID}
	activity, err := manager.networkActivityMonitor.LastNetworkActivity(ctx, request)
	if err != nil {
		return NetworkActivity{}, fmt.Errorf("read network activity for VM %s: %w", desired.ID, err)
	}
	return activity, nil
}
