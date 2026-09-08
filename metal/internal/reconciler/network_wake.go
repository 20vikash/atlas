package reconciler

import (
	"context"
	"log/slog"
	"time"

	"github.com/frappe/atlas/metal/internal/vm"
)

// defaultNetworkWakeTimeout bounds one wake restore.
const defaultNetworkWakeTimeout = 2 * time.Minute

// NetworkWakeManager restores a sleeping VM after a network wake event.
type NetworkWakeManager interface {
	WakeFromNetwork(ctx context.Context, event vm.NetworkWakeEvent) error
}

// NetworkWakeConfig controls the wake worker.
type NetworkWakeConfig struct {
	// Logger receives wake failures.
	Logger *slog.Logger
	// OperationTimeout bounds one wake restore.
	OperationTimeout time.Duration
}

// NetworkWakeReconciler restores sleeping VMs from packet wake events.
type NetworkWakeReconciler struct {
	manager          NetworkWakeManager
	events           <-chan vm.NetworkWakeEvent
	operationTimeout time.Duration
	logger           *slog.Logger
}

// NewNetworkWakeReconciler returns a wake reconciler for one event channel.
func NewNetworkWakeReconciler(
	manager NetworkWakeManager,
	events <-chan vm.NetworkWakeEvent,
	configuration NetworkWakeConfig,
) *NetworkWakeReconciler {
	if configuration.OperationTimeout <= 0 {
		configuration.OperationTimeout = defaultNetworkWakeTimeout
	}
	if configuration.Logger == nil {
		configuration.Logger = slog.Default()
	}

	return &NetworkWakeReconciler{
		manager:          manager,
		events:           events,
		operationTimeout: configuration.OperationTimeout,
		logger:           configuration.Logger,
	}
}

// Run restores VMs from wake events until ctx is canceled or the channel closes.
func (r *NetworkWakeReconciler) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-r.events:
			if !open {
				return
			}
			r.wake(ctx, event)
		}
	}
}

// wake restores one VM under the operation timeout.
func (r *NetworkWakeReconciler) wake(ctx context.Context, event vm.NetworkWakeEvent) {
	operationContext, cancel := context.WithTimeout(ctx, r.operationTimeout)
	defer cancel()

	if err := r.manager.WakeFromNetwork(operationContext, event); err != nil {
		r.logFailure(ctx, "network wake failed", err,
			"virtual_machine_id", event.VirtualMachineID, "user_id", event.UserID)
	}
}

// logFailure reports errors from active workers.
func (r *NetworkWakeReconciler) logFailure(ctx context.Context, message string, err error, fields ...any) {
	if ctx.Err() != nil {
		return
	}

	r.logger.Error(message, append([]any{"error", err}, fields...)...)
}
