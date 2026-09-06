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

// SerialBroker owns the serial consoles of all running virtual machines.
type SerialBroker struct {
	directory       string
	scrollbackBytes int

	mutex    sync.Mutex
	consoles map[string]*console
}

// NewSerialBroker returns a SerialBroker that keeps the PTY slave links under directory.
func NewSerialBroker(directory string) *SerialBroker {
	return &SerialBroker{
		directory:       directory,
		scrollbackBytes: defaultScrollbackBytes,
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

	link := filepath.Join(b.directory, id)
	if err := b.linkSlave(slave.Name(), link); err != nil {
		master.Close()
		return err
	}

	b.consoles[id] = newConsole(master, link, b.scrollbackBytes)

	return nil
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

	return openConsole.close()
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

// Shutdown closes every open console.
func (b *SerialBroker) Shutdown() {
	b.mutex.Lock()
	openConsoles := b.consoles
	b.consoles = make(map[string]*console)
	b.mutex.Unlock()

	for _, openConsole := range openConsoles {
		_ = openConsole.close()
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
