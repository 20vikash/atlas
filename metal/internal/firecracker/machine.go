package firecracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

// defaultStopTimeout is how long a guest has to shut itself down after
// Ctrl+Alt+Del before it is killed.
const defaultStopTimeout = 30 * time.Second

// machine binds a runtime to one VM for the length of an operation. State comes
// from two places: systemd owns whether the process runs, and Firecracker owns
// what the guest is doing inside it.
type machine struct {
	runtime     *Runtime
	input       vm.RuntimeMachine
	api         *api.Client
	stopTimeout time.Duration
}

// newMachine returns a handle for one VM.
func (runtime *Runtime) newMachine(input vm.RuntimeMachine) *machine {
	return &machine{
		runtime:     runtime,
		input:       input,
		api:         api.New(runtime.configuration.socketPath(input.ID)),
		stopTimeout: defaultStopTimeout,
	}
}

// status reports the VM state from the unit and the guest.
func (m *machine) status(ctx context.Context) (vm.State, error) {
	unitStatus, err := m.runtime.units.Status(ctx, m.input.ID)
	if err != nil {
		return "", err
	}

	return m.state(ctx, unitStatus)
}

// state maps a unit state to a VM state. Only an active unit has a guest worth
// asking, so the other unit states answer on their own.
func (m *machine) state(ctx context.Context, status platform.Status) (vm.State, error) {
	switch status.ActiveState {
	case "failed":
		return vm.StateFailed, nil
	case "inactive", "deactivating":
		return vm.StateStopped, nil
	case "active":
	default:
		return vm.StateUnknown, nil
	}

	instance, err := m.api.InstanceInfo(ctx)
	if err != nil {
		return vm.StateUnknown, fmt.Errorf("inspect Firecracker instance: %w", err)
	}

	switch instance.State {
	case "Not started":
		return vm.StateCreated, nil
	case "Running":
		return vm.StateRunning, nil
	case "Paused":
		return vm.StatePaused, nil
	default:
		return vm.StateUnknown, nil
	}
}

// Start boots a VM with the shared warm image if the image asks for it, and
// falls back to a cold boot, because warm boot is an optimization and cold boot
// is the reliable path. It never resumes a VM-local snapshot on its own: a resume
// happens only through StartFromSleepSnapshot, which the manager selects when the
// VM was warm-stopped. So a VM stopped outside the API cold boots. A failed warm
// boot releases the disk it prepared before starting again.
func (m *machine) Start(ctx context.Context) error {
	if m.runtime.hasMatchingMemorySnapshot(m.input.Specification) {
		err := m.runtime.launchWarmImage(ctx, m.input, m.input.Specification.Image.Name)
		if err == nil {
			m.recordImageUse()
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		m.runtime.logger.Warn("warm boot failed, using cold boot", "virtual_machine_id", m.input.ID, "error", err)
		if err := m.runtime.virtualMachineStorage.Release(ctx, m.input.ID); err != nil {
			return err
		}
	}

	return m.coldBoot(ctx)
}

// coldBoot discards the previous jail and boots the guest fresh.
func (m *machine) coldBoot(ctx context.Context) error {
	if err := m.runtime.relaunch(ctx, m.input); err != nil {
		return err
	}
	if err := m.api.InstanceStart(ctx); err != nil {
		return err
	}
	m.recordImageUse()

	return nil
}

// snapshotRequirement is what this VM needs a published snapshot to be, for a
// restore or a completion check.
func (m *machine) snapshotRequirement() snapshotRequirement {
	return snapshotRequirement{
		VirtualMachineID:         m.input.ID,
		UserID:                   m.input.UserID,
		SpecificationGeneration:  m.input.SpecificationGeneration,
		RestartGeneration:        m.input.RestartGeneration,
		FirecrackerCompatibility: m.runtime.firecrackerCompatibility(),
	}
}

// restoreSnapshot restores the newest valid VM-local snapshot and reports whether
// one was found. A restore reuses the VM disk, which Firecracker synced during
// snapshot creation, so it does not clone a new one. When resume is false the
// guest is left paused. After a successful load the jail holds its own copy of
// the memory file, so every external generation is removed.
func (m *machine) restoreSnapshot(ctx context.Context, resume bool) (bool, error) {
	snapshot, err := m.runtime.configuration.latestValidSnapshot(m.snapshotRequirement())
	if errors.Is(err, errSnapshotNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	metadata := metadataServiceData(
		m.input.ID, m.input.NetworkInterface.GuestIPAddress, m.input.NetworkInterface.MACAddress, m.input.Specification,
	)
	if err := m.runtime.launchSnapshot(ctx, m.input, "", snapshot.StatePath, snapshot.MemoryPath, metadata, resume); err != nil {
		return false, err
	}
	if err := m.runtime.purgeSnapshots(m.input.ID); err != nil {
		m.runtime.logger.Warn("remove restored snapshots failed", "virtual_machine_id", m.input.ID, "error", err)
	}

	return true, nil
}

// hasCompleteSnapshot reports whether a valid published snapshot exists for this
// VM at its current generations and Firecracker build.
func (m *machine) hasCompleteSnapshot() bool {
	_, err := m.runtime.configuration.latestValidSnapshot(m.snapshotRequirement())
	return err == nil
}

// startFromSnapshot restores the VM-local snapshot and requires one to exist. It
// never falls back to a cold boot, because a caller that asked for a snapshot
// restore must not silently lose the saved guest memory. When resume is false the
// guest is left paused.
func (m *machine) startFromSnapshot(ctx context.Context, resume bool) error {
	restored, err := m.restoreSnapshot(ctx, resume)
	if err != nil {
		return err
	}
	if !restored {
		return fmt.Errorf("start from sleep snapshot: %w", errSnapshotNotFound)
	}
	m.recordImageUse()

	return nil
}

// Stop shuts the guest down and clears the unit failure the exit records. It also
// discards any memory snapshot, so a later start cold boots instead of resuming a
// state the caller asked to leave.
func (m *machine) Stop(ctx context.Context) error {
	if err := m.shutdownGuest(ctx); err != nil {
		return err
	}
	if _, err := m.runtime.units.Wait(ctx, m.input.ID); err != nil {
		return err
	}
	_ = m.runtime.serialBroker.Close(m.input.ID)

	if err := m.runtime.purgeSnapshots(m.input.ID); err != nil {
		return err
	}

	// An intentional stop leaves the unit failed, because the process was killed.
	return m.runtime.units.ResetFailed(ctx, m.input.ID)
}

// warmStop pauses the guest, publishes a full snapshot, and terminates
// Firecracker, so a later start can resume it. The snapshot is published before
// termination, so an interrupted warm stop stays recoverable: a later start
// restores the published snapshot. The VM must be running or paused.
func (m *machine) warmStop(ctx context.Context) error {
	state, err := m.status(ctx)
	if err != nil {
		return err
	}

	// Recover a warm stop that already published a snapshot. metald may have
	// crashed after publication but before termination.
	if m.hasCompleteSnapshot() {
		switch state {
		case vm.StateStopped:
			return nil
		case vm.StatePaused:
			return m.kill(ctx)
		default:
			// A running VM with a complete snapshot is inconsistent: a successful
			// warm stop leaves the process paused or gone, never running.
			return vm.ErrConflict
		}
	}

	pausedByThisCall := false
	switch state {
	case vm.StateRunning:
		if err := m.api.Pause(ctx); err != nil {
			return fmt.Errorf("pause for warm stop: %w", err)
		}
		pausedByThisCall = true
	case vm.StatePaused:
		// An already paused guest needs no second pause.
	default:
		return vm.ErrConflict
	}

	if _, err := m.runtime.createAndPublishSnapshot(ctx, m.input); err != nil {
		return m.recoverFailedWarmStop(ctx, pausedByThisCall, err)
	}

	return m.kill(ctx)
}

// recoverFailedWarmStop cleans up a failed warm stop. It removes the current
// pending generation and resumes the guest only when this call paused it, so an
// originally paused VM stays paused. It joins any cleanup or resume error with
// the original failure.
func (m *machine) recoverFailedWarmStop(ctx context.Context, pausedByThisCall bool, cause error) error {
	recovery := []error{cause}
	if err := os.RemoveAll(m.runtime.configuration.pendingSnapshotDirectory(m.input.ID)); err != nil {
		recovery = append(recovery, fmt.Errorf("remove pending snapshot: %w", err))
	}
	if pausedByThisCall {
		if err := m.api.Resume(ctx); err != nil {
			recovery = append(recovery, fmt.Errorf("resume after failed warm stop: %w", err))
		}
	}
	return errors.Join(recovery...)
}

// Pause halts the guest virtual CPUs.
func (m *machine) Pause(ctx context.Context) error {
	state, err := m.status(ctx)
	if err != nil {
		return err
	}
	if state != vm.StateRunning {
		return vm.ErrConflict
	}

	return m.api.Pause(ctx)
}

// Resume returns a paused guest to the running state.
func (m *machine) Resume(ctx context.Context) error {
	state, err := m.status(ctx)
	if err != nil {
		return err
	}
	if state != vm.StatePaused {
		return vm.ErrConflict
	}

	return m.api.Resume(ctx)
}

// kill stops a VM without giving the guest a chance to shut down.
func (m *machine) kill(ctx context.Context) error {
	if err := m.runtime.units.Kill(ctx, m.input.ID, syscall.SIGKILL); err != nil {
		return err
	}
	if _, err := m.runtime.units.Wait(ctx, m.input.ID); err != nil {
		return err
	}
	_ = m.runtime.serialBroker.Close(m.input.ID)

	return m.runtime.units.ResetFailed(ctx, m.input.ID)
}

// shutdownGuest asks the guest to power off and kills it when it does not. A
// guest with no ACPI handler never answers Ctrl+Alt+Del, so the wait is bounded.
func (m *machine) shutdownGuest(ctx context.Context) error {
	if m.api.SendCtrlAltDel(ctx) == nil {
		wait, cancel := context.WithTimeout(ctx, m.stopTimeout)
		defer cancel()

		if _, err := m.runtime.units.Wait(wait, m.input.ID); err == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	return m.runtime.units.Kill(ctx, m.input.ID, syscall.SIGKILL)
}

// cleanupSystemd stops the unit and clears its state, so the ID can be reused.
func (m *machine) cleanupSystemd(ctx context.Context) error {
	if err := m.runtime.units.Stop(ctx, m.input.ID); err != nil {
		return fmt.Errorf("stop VM unit: %w", err)
	}
	_ = m.runtime.serialBroker.Close(m.input.ID)

	if err := m.runtime.units.ResetFailed(ctx, m.input.ID); err != nil {
		return fmt.Errorf("reset VM unit: %w", err)
	}

	return nil
}

// recordImageUse marks the image as used, so pruning keeps it. A failure here
// must not fail a started VM, so it is logged instead.
func (m *machine) recordImageUse() {
	if err := m.runtime.imageStore.RecordImageUse(m.input.Specification.Image.Name, time.Now()); err != nil {
		m.runtime.logger.Error("record image use failed", "virtual_machine_id", m.input.ID, "error", err)
	}
}
