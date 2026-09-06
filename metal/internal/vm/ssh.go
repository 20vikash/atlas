package vm

import "io"

// SSHConnection is an interactive SSH session to a guest.
type SSHConnection interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
}
