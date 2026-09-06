package api

import (
	"context"
	"io"
	"sync"

	"github.com/coder/websocket"

	"github.com/frappe/atlas/metal/internal/console"
	"github.com/frappe/atlas/metal/internal/vm"
)

// streamSSHConsole opens an SSH session to the guest for one viewer. The session
// belongs to this WebSocket and ends with it.
func (s *Server) streamSSHConsole(ctx context.Context, connection *websocket.Conn, id string) {
	session, err := s.virtualMachineManager.ConnectSSH(ctx, id)
	if err != nil {
		connection.Close(websocket.StatusGoingAway, "ssh session unavailable")
		return
	}

	var once sync.Once
	closeSession := func() { once.Do(func() { _ = session.Close() }) }
	defer closeSession()

	terminal := newWebSocketTerminal(ctx, connection)
	go forwardResize(ctx, terminal.resize, session)

	// A viewer that disconnects ends this copy. Closing the session here releases
	// the guest process, the temporary key, and the session slot, and it unblocks
	// the read below. Without it a quiet guest would hold all three open.
	go func() {
		defer closeSession()
		_, _ = io.Copy(session, terminal)
	}()

	_, _ = io.Copy(terminal, session)
	connection.Close(websocket.StatusNormalClosure, "")
}

// forwardResize applies viewer resize requests to the SSH session until ctx ends.
func forwardResize(ctx context.Context, requests <-chan console.Winsize, session vm.SSHConnection) {
	for {
		select {
		case <-ctx.Done():
			return
		case size := <-requests:
			_ = session.Resize(size.Cols, size.Rows)
		}
	}
}
