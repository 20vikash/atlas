package platform

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"

	"github.com/coreos/go-systemd/v22/activation"
)

// notificationSocketEnvironmentVariable names the systemd notification socket variable.
const notificationSocketEnvironmentVariable = "NOTIFY_SOCKET"

// ErrInvalidStoreName reports a descriptor name that systemd cannot accept.
var ErrInvalidStoreName = errors.New("platform: invalid file descriptor store name")

// FileDescriptorStore keeps file descriptors in systemd across a service restart.
type FileDescriptorStore struct {
	notificationSocketAddress string
}

// NewFileDescriptorStore reads the systemd notification socket.
func NewFileDescriptorStore() *FileDescriptorStore {
	return &FileDescriptorStore{notificationSocketAddress: os.Getenv(notificationSocketEnvironmentVariable)}
}

// IsAvailable reports whether systemd offers a file descriptor store.
func (s *FileDescriptorStore) IsAvailable() bool {
	return s.notificationSocketAddress != ""
}

// Store adds file to the store under name.
func (s *FileDescriptorStore) Store(name string, file *os.File) error {
	if err := validateDescriptorName(name); err != nil {
		return err
	}

	return s.sendDescriptorStoreNotification("FDSTORE=1\nFDNAME="+name, file)
}

// Remove drops every descriptor that systemd holds under name.
func (s *FileDescriptorStore) Remove(name string) error {
	if err := validateDescriptorName(name); err != nil {
		return err
	}

	return s.sendDescriptorStoreNotification("FDSTOREREMOVE=1\nFDNAME="+name, nil)
}

// TakeFiles returns descriptors from systemd, keyed by name.
func (s *FileDescriptorStore) TakeFiles() map[string][]*os.File {
	return activation.FilesWithNames()
}

// sendDescriptorStoreNotification sends one descriptor-store message.
func (s *FileDescriptorStore) sendDescriptorStoreNotification(notificationMessage string, descriptorFile *os.File) error {
	if !s.IsAvailable() {
		return nil
	}

	// systemd uses "@" for an abstract socket address.
	notificationSocketAddress := s.notificationSocketAddress
	if strings.HasPrefix(notificationSocketAddress, "@") {
		notificationSocketAddress = "\x00" + notificationSocketAddress[1:]
	}

	notificationConnection, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: notificationSocketAddress, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("dial notification socket: %w", err)
	}
	defer notificationConnection.Close()

	if descriptorFile == nil {
		if _, _, err := notificationConnection.WriteMsgUnix([]byte(notificationMessage), nil, nil); err != nil {
			return fmt.Errorf("send notification message: %w", err)
		}

		return nil
	}

	// Control preserves the Go runtime poller registration.
	rawFileDescriptor, err := descriptorFile.SyscallConn()
	if err != nil {
		return fmt.Errorf("read file descriptor: %w", err)
	}

	var notificationError error
	sendDescriptor := func(fileDescriptor uintptr) {
		rights := syscall.UnixRights(int(fileDescriptor))
		_, _, notificationError = notificationConnection.WriteMsgUnix([]byte(notificationMessage), rights, nil)
	}
	if err := rawFileDescriptor.Control(sendDescriptor); err != nil {
		return fmt.Errorf("access file descriptor: %w", err)
	}
	if notificationError != nil {
		return fmt.Errorf("send notification message: %w", notificationError)
	}

	return nil
}

// validateDescriptorName rejects an invalid systemd descriptor name.
func validateDescriptorName(name string) error {
	if name == "" || strings.ContainsAny(name, ":\n\x00") {
		return fmt.Errorf("%w: %q", ErrInvalidStoreName, name)
	}

	return nil
}
