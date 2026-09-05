package firecracker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/vm"
)

// sshConsoleUser is the guest account used by the SSH console.
const sshConsoleUser = "root"

// ConnectSSH opens an interactive SSH session to a VM guest.
func (d *Runtime) ConnectSSH(ctx context.Context, input vm.RuntimeMachine) (vm.SSHConn, error) {
	select {
	case d.sshSlots <- struct{}{}:
	default:
		return nil, fmt.Errorf("ssh session limit reached")
	}
	releaseSlot := func() { <-d.sshSlots }

	keyPair, err := generateSSHKey()
	if err != nil {
		releaseSlot()
		return nil, err
	}
	index, err := sshConsoleKeyIndex()
	if err != nil {
		releaseSlot()
		return nil, err
	}

	client := api.New(d.configuration.sockPath(input.ID))
	if err := d.authorizeSSHConsoleKey(ctx, client, index, strings.TrimSpace(keyPair.authorizedKey)); err != nil {
		releaseSlot()
		return nil, fmt.Errorf("authorize ssh key: %w", err)
	}
	removeKey := func() {
		_ = d.authorizeSSHConsoleKey(context.WithoutCancel(ctx), client, index, nil)
	}

	namespace := filepath.Base(input.NetworkInterface.NetworkNamespacePath)
	session, err := startSSHSession(ctx, d.configuration.SocketsDir, namespace, sshConsoleUser, input.NetworkInterface.GuestIPAddress, keyPair.privatePEM)
	if err != nil {
		removeKey()
		releaseSlot()
		return nil, err
	}
	return &sshConsoleConnection{session: session, cleanup: func() {
		removeKey()
		releaseSlot()
	}}, nil
}

// authorizeSSHConsoleKey adds or removes one SSH console key in MMDS.
func (d *Runtime) authorizeSSHConsoleKey(ctx context.Context, client *api.Client, index string, authorizedKey any) error {
	patch := map[string]any{
		"latest": map[string]any{
			"meta-data": map[string]any{
				"public-keys": map[string]any{index: sshConsoleKeyValue(authorizedKey)},
			},
		},
	}
	return client.PatchMMDS(ctx, patch)
}

func sshConsoleKeyValue(authorizedKey any) any {
	if authorizedKey == nil {
		return nil
	}
	return map[string]any{"openssh-key": authorizedKey}
}

func sshConsoleKeyIndex() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("ssh key index: %w", err)
	}
	return "console-" + hex.EncodeToString(buffer), nil
}

// sshConsoleConnection runs cleanup when the SSH session closes.
type sshConsoleConnection struct {
	session *sshSession
	cleanup func()
}

func (c *sshConsoleConnection) Read(buffer []byte) (int, error)  { return c.session.Read(buffer) }
func (c *sshConsoleConnection) Write(buffer []byte) (int, error) { return c.session.Write(buffer) }
func (c *sshConsoleConnection) Resize(cols, rows uint16) error   { return c.session.Resize(cols, rows) }

func (c *sshConsoleConnection) Close() error {
	err := c.session.Close()
	c.cleanup()
	return err
}
