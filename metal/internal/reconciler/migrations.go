package reconciler

import (
	"context"
	"log/slog"
	"time"
)

// defaultMigrationOperationTimeout bounds one migration handshake step. The step
// makes one call to the source and writes local records, so the limit is short.
const defaultMigrationOperationTimeout = 5 * time.Minute

// MigrationDriver lists and advances active target migrations.
type MigrationDriver interface {
	ActiveTargetVirtualMachineIDs(ctx context.Context) ([]string, error)
	AdvanceTarget(ctx context.Context, virtualMachineID string) error
}

// MigrationConfig controls one migration reconcile pass.
type MigrationConfig struct {
	Logger           *slog.Logger
	OperationTimeout time.Duration
}

// MigrationReconciler advances every active target migration on an interval and
// on demand. metald owns its goroutine and wakes it after a migration request.
type MigrationReconciler struct {
	passScheduler

	driver           MigrationDriver
	operationTimeout time.Duration
	logger           *slog.Logger
}

// NewMigrationReconciler returns a migration reconciler that runs at interval.
func NewMigrationReconciler(driver MigrationDriver, interval time.Duration, configuration MigrationConfig) *MigrationReconciler {
	if configuration.OperationTimeout <= 0 {
		configuration.OperationTimeout = defaultMigrationOperationTimeout
	}
	if configuration.Logger == nil {
		configuration.Logger = slog.Default()
	}
	return &MigrationReconciler{
		passScheduler:    newPassScheduler(interval),
		driver:           driver,
		operationTimeout: configuration.OperationTimeout,
		logger:           configuration.Logger,
	}
}

// Run advances migrations until ctx is canceled.
func (r *MigrationReconciler) Run(ctx context.Context) {
	r.run(ctx, r.advanceAll)
}

// advanceAll advances every active target migration once.
func (r *MigrationReconciler) advanceAll(ctx context.Context) {
	listContext, cancelList := context.WithTimeout(ctx, r.operationTimeout)
	virtualMachineIDs, err := r.driver.ActiveTargetVirtualMachineIDs(listContext)
	cancelList()
	if err != nil {
		r.logFailure(ctx, "migration reconciler list failed", err)
		return
	}

	for _, virtualMachineID := range virtualMachineIDs {
		if ctx.Err() != nil {
			return
		}
		r.advance(ctx, virtualMachineID)
	}
}

// advance runs one migration handshake step under the operation timeout.
func (r *MigrationReconciler) advance(ctx context.Context, virtualMachineID string) {
	operationContext, cancel := context.WithTimeout(ctx, r.operationTimeout)
	defer cancel()

	if err := r.driver.AdvanceTarget(operationContext, virtualMachineID); err != nil {
		r.logFailure(ctx, "migration handshake failed", err, "virtual_machine_id", virtualMachineID)
	}
}

// logFailure reports an error unless ctx ended, because a canceled pass fails
// every step still in flight and those errors say nothing about the host.
func (r *MigrationReconciler) logFailure(ctx context.Context, message string, err error, fields ...any) {
	if ctx.Err() != nil {
		return
	}
	r.logger.Error(message, append([]any{"error", err}, fields...)...)
}
