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

// defaultStopTimeout bounds graceful guest shutdown.
const defaultStopTimeout = 30 * time.Second

// machine binds a runtime to one VM for one operation.
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

// state maps a systemd unit state to a VM state.
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

// Start boots a VM with a warm image when available, then falls back to cold boot.
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

// memorySnapshotRequirement describes a snapshot required by this VM.
func (m *machine) memorySnapshotRequirement() memorySnapshotRequirement {
	return memorySnapshotRequirement{
		VirtualMachineID:         m.input.ID,
		UserID:                   m.input.UserID,
		SpecificationGeneration:  m.input.SpecificationGeneration,
		RestartGeneration:        m.input.RestartGeneration,
		FirecrackerCompatibility: m.runtime.firecrackerCompatibility(),
	}
}

// restoreMemorySnapshot restores the newest valid VM-local snapshot.
func (m *machine) restoreMemorySnapshot(ctx context.Context, resume bool) (bool, error) {
	snapshot, err := m.runtime.configuration.latestValidMemorySnapshot(m.memorySnapshotRequirement())
	if errors.Is(err, errMemorySnapshotNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	metadata := metadataServiceData(
		m.input.ID, m.input.NetworkInterface.GuestIPAddress, m.input.NetworkInterface.MACAddress, m.input.Specification,
	)
	if err := m.runtime.launchMemorySnapshot(ctx, m.input, "", snapshot.StatePath, snapshot.MemoryPath, metadata, resume); err != nil {
		return false, err
	}
	if err := m.runtime.purgeMemorySnapshots(m.input.ID); err != nil {
		m.runtime.logger.Warn("remove restored snapshots failed", "virtual_machine_id", m.input.ID, "error", err)
	}

	return true, nil
}

// latestMemorySnapshot returns the newest valid snapshot.
func (m *machine) latestMemorySnapshot() (validatedMemorySnapshot, bool) {
	snapshot, err := m.runtime.configuration.latestValidMemorySnapshot(m.memorySnapshotRequirement())
	if err != nil {
		return validatedMemorySnapshot{}, false
	}
	return snapshot, true
}

// startFromMemorySnapshot restores a required VM-local snapshot without cold booting.
func (m *machine) startFromMemorySnapshot(ctx context.Context, resume bool) error {
	restored, err := m.restoreMemorySnapshot(ctx, resume)
	if err != nil {
		return err
	}
	if !restored {
		return fmt.Errorf("start from sleep snapshot: %w", errMemorySnapshotNotFound)
	}
	m.recordImageUse()

	return nil
}

// Stop shuts down the guest and discards its memory snapshot.
func (m *machine) Stop(ctx context.Context) error {
	if err := m.shutdownGuest(ctx); err != nil {
		return err
	}
	if _, err := m.runtime.units.Wait(ctx, m.input.ID); err != nil {
		return err
	}
	_ = m.runtime.serialBroker.Close(m.input.ID)

	if err := m.runtime.purgeMemorySnapshots(m.input.ID); err != nil {
		return err
	}

	// An intentional stop leaves the unit failed, because the process was killed.
	return m.runtime.units.ResetFailed(ctx, m.input.ID)
}

// warmStop publishes a snapshot before terminating Firecracker.
func (m *machine) warmStop(ctx context.Context) (vm.StopOutcome, error) {
	state, err := m.status(ctx)
	if err != nil {
		return vm.StopOutcome{}, err
	}

	// Recover a snapshot published before a daemon crash.
	if snapshot, ok := m.latestMemorySnapshot(); ok {
		switch state {
		case vm.StateStopped:
			return stopOutcome(snapshot), nil
		case vm.StatePaused:
			if err := m.kill(ctx); err != nil {
				return vm.StopOutcome{}, err
			}
			return stopOutcome(snapshot), nil
		default:
			// A complete snapshot and a running VM are inconsistent.
			return vm.StopOutcome{}, vm.ErrConflict
		}
	}

	pausedByThisCall := false
	switch state {
	case vm.StateRunning:
		if err := m.api.Pause(ctx); err != nil {
			return vm.StopOutcome{}, fmt.Errorf("pause for warm stop: %w", err)
		}
		pausedByThisCall = true
	case vm.StatePaused:
		// An already paused guest needs no second pause.
	default:
		return vm.StopOutcome{}, vm.ErrConflict
	}

	published, err := m.runtime.createAndPublishMemorySnapshot(ctx, m.input)
	if err != nil {
		return vm.StopOutcome{}, m.recoverFailedWarmStop(ctx, pausedByThisCall, err)
	}

	if err := m.kill(ctx); err != nil {
		return vm.StopOutcome{}, err
	}

	return stopOutcome(published), nil
}

// stopOutcome reports the published generation and creation time of a snapshot.
func stopOutcome(snapshot validatedMemorySnapshot) vm.StopOutcome {
	return vm.StopOutcome{
		MemorySnapshotGeneration: snapshot.Generation,
		MemorySnapshotCreatedAt:  snapshot.Manifest.CreatedAt,
	}
}

// recoverFailedWarmStop removes the pending snapshot and restores the prior state.
func (m *machine) recoverFailedWarmStop(ctx context.Context, pausedByThisCall bool, cause error) error {
	recovery := []error{cause}
	if err := os.RemoveAll(m.runtime.configuration.pendingMemorySnapshotDirectory(m.input.ID)); err != nil {
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

// shutdownGuest asks the guest to power off, then kills it after a timeout.
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

// recordImageUse marks the image as used without failing a started VM.
func (m *machine) recordImageUse() {
	if err := m.runtime.imageStore.RecordImageUse(m.input.Specification.Image.Name, time.Now()); err != nil {
		m.runtime.logger.Error("record image use failed", "virtual_machine_id", m.input.ID, "error", err)
	}
}
