// Package console manages VM serial consoles over PTYs.
package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

// defaultScrollbackBytes is the console history a new viewer receives.
const defaultScrollbackBytes = 128 << 10

var (
	// ErrConsoleNotFound reports that a VM has no open console.
	ErrConsoleNotFound = errors.New("console: not found")

	// ErrConsoleBusy reports that a console has too many viewers.
	ErrConsoleBusy = errors.New("console: too many viewers")
)

// Winsize is a viewer terminal size applied to the PTY.
type Winsize struct {
	Rows uint16
	Cols uint16
}

// DescriptorStore keeps PTY masters across a process restart.
type DescriptorStore interface {
	Store(name string, file *os.File) error
	Remove(name string) error
	TakeFiles() map[string][]*os.File
}

// SerialBroker owns the serial consoles of all running virtual machines.
type SerialBroker struct {
	directory       string
	scrollbackBytes int
	descriptorStore DescriptorStore

	mutex    sync.Mutex
	consoles map[string]*console
}

// NewSerialBroker returns a SerialBroker with links in directory and masters in descriptorStore.
func NewSerialBroker(directory string, descriptorStore DescriptorStore) *SerialBroker {
	return &SerialBroker{
		directory:       directory,
		scrollbackBytes: defaultScrollbackBytes,
		descriptorStore: descriptorStore,
		consoles:        make(map[string]*console),
	}
}

// Open allocates a PTY for a VM and links its slave to a stable path.
// Open is idempotent.
func (b *SerialBroker) Open(id string) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if _, found := b.consoles[id]; found {
		return nil
	}

	master, slave, err := pty.Open()
	if err != nil {
		return fmt.Errorf("open console pty: %w", err)
	}
	// Keep only the master. systemd reopens the slave through the link.
	defer slave.Close()

	master, err = newNonBlockingMaster(master)
	if err != nil {
		return err
	}

	link := filepath.Join(b.directory, id)
	if err := b.linkSlave(slave.Name(), link); err != nil {
		master.Close()
		return err
	}

	// Keep one stored descriptor for each VM.
	if err := b.descriptorStore.Remove(id); err != nil {
		return b.closeFailedConsole(master, link, fmt.Errorf("clear stored console descriptor: %w", err))
	}
	if err := b.descriptorStore.Store(id, master); err != nil {
		return b.closeFailedConsole(master, link, fmt.Errorf("store console descriptor: %w", err))
	}

	b.consoles[id] = newConsole(master, link, b.scrollbackBytes)

	return nil
}

// Adopt restores consoles for running virtual machines.
func (b *SerialBroker) Adopt(runningVirtualMachineIDs []string) int {
	runningVirtualMachines := make(map[string]struct{}, len(runningVirtualMachineIDs))
	for _, virtualMachineID := range runningVirtualMachineIDs {
		runningVirtualMachines[virtualMachineID] = struct{}{}
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()

	for virtualMachineID, masterFiles := range b.descriptorStore.TakeFiles() {
		if _, found := runningVirtualMachines[virtualMachineID]; !found || len(masterFiles) == 0 {
			b.removeStaleConsole(virtualMachineID, masterFiles)
			continue
		}

		b.consoles[virtualMachineID] = newConsole(masterFiles[0], filepath.Join(b.directory, virtualMachineID), b.scrollbackBytes)
		// One VM has one console.
		closeFiles(masterFiles[1:])
	}
	b.removeLinksWithoutConsoles()

	return len(b.consoles)
}

// Close stops and removes a VM's console. Close is idempotent.
func (b *SerialBroker) Close(id string) error {
	b.mutex.Lock()
	openConsole := b.consoles[id]
	delete(b.consoles, id)
	b.mutex.Unlock()

	if openConsole == nil {
		return nil
	}

	return errors.Join(b.descriptorStore.Remove(id), openConsole.close())
}

// Attach streams a VM's console to one viewer until it disconnects.
func (b *SerialBroker) Attach(ctx context.Context, id string, client io.ReadWriter, resize <-chan Winsize) error {
	b.mutex.Lock()
	openConsole := b.consoles[id]
	b.mutex.Unlock()

	if openConsole == nil {
		return ErrConsoleNotFound
	}

	return openConsole.attach(ctx, client, resize)
}

// Shutdown releases every console. The stored descriptors keep the PTYs open.
func (b *SerialBroker) Shutdown() {
	b.mutex.Lock()
	openConsoles := b.consoles
	b.consoles = make(map[string]*console)
	b.mutex.Unlock()

	for _, openConsole := range openConsoles {
		_ = openConsole.release()
	}
}

// newNonBlockingMaster returns master as a non-blocking file. The Go runtime
// then interrupts a blocked drain read when the console releases the master.
func newNonBlockingMaster(master *os.File) (*os.File, error) {
	defer master.Close()

	duplicate, _, errorNumber := syscall.Syscall(syscall.SYS_FCNTL, master.Fd(), syscall.F_DUPFD_CLOEXEC, 0)
	if errorNumber != 0 {
		return nil, fmt.Errorf("duplicate console master: %w", errorNumber)
	}
	if err := syscall.SetNonblock(int(duplicate), true); err != nil {
		_ = syscall.Close(int(duplicate))
		return nil, fmt.Errorf("set console master non-blocking: %w", err)
	}

	return os.NewFile(duplicate, master.Name()), nil
}

// closeFailedConsole removes a failed console setup.
func (b *SerialBroker) closeFailedConsole(master *os.File, link string, cause error) error {
	master.Close()
	_ = os.Remove(link)

	return cause
}

// removeStaleConsole releases a stale console and its master files.
func (b *SerialBroker) removeStaleConsole(virtualMachineID string, masterFiles []*os.File) {
	closeFiles(masterFiles)
	_ = b.descriptorStore.Remove(virtualMachineID)
	_ = os.Remove(filepath.Join(b.directory, virtualMachineID))
}

// closeFiles closes every PTY master file.
func closeFiles(masterFiles []*os.File) {
	for _, masterFile := range masterFiles {
		_ = masterFile.Close()
	}
}

// removeLinksWithoutConsoles deletes links without consoles.
func (b *SerialBroker) removeLinksWithoutConsoles() {
	entries, err := os.ReadDir(b.directory)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if _, found := b.consoles[entry.Name()]; !found {
			_ = os.Remove(filepath.Join(b.directory, entry.Name()))
		}
	}
}

// linkSlave replaces any stale link so the unit always finds the current slave.
func (b *SerialBroker) linkSlave(slaveName, link string) error {
	if err := os.MkdirAll(b.directory, 0o750); err != nil {
		return fmt.Errorf("create console directory: %w", err)
	}
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear console link: %w", err)
	}
	if err := os.Symlink(slaveName, link); err != nil {
		return fmt.Errorf("link console slave: %w", err)
	}

	return nil
}
