package platform

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/coreos/go-systemd/v22/activation"
)

// notificationSocketEnvironmentVariable names the systemd notification socket variable.
const notificationSocketEnvironmentVariable = "NOTIFY_SOCKET"

// ErrInvalidStoreName reports a descriptor name that systemd cannot accept.
var ErrInvalidStoreName = errors.New("platform: invalid file descriptor store name")

// FileDescriptorStore keeps service file descriptors in systemd.
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

	notificationSocket, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open notification socket: %w", err)
	}
	defer syscall.Close(notificationSocket)

	notificationAddress := &syscall.SockaddrUnix{
		Name: notificationSocketName(s.notificationSocketAddress),
	}

	if descriptorFile == nil {
		if _, err := syscall.SendmsgN(notificationSocket, []byte(notificationMessage), nil, notificationAddress, 0); err != nil {
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
		_, notificationError = syscall.SendmsgN(
			notificationSocket,
			[]byte(notificationMessage),
			rights,
			notificationAddress,
			0,
		)
	}
	if err := rawFileDescriptor.Control(sendDescriptor); err != nil {
		return fmt.Errorf("access file descriptor: %w", err)
	}
	if notificationError != nil {
		return fmt.Errorf("send notification message: %w", notificationError)
	}

	return nil
}

// notificationSocketName converts the systemd abstract socket notation.
func notificationSocketName(notificationSocketAddress string) string {
	if strings.HasPrefix(notificationSocketAddress, "@") {
		return "\x00" + notificationSocketAddress[1:]
	}

	return notificationSocketAddress
}

// validateDescriptorName rejects an invalid systemd descriptor name.
func validateDescriptorName(name string) error {
	if name == "" || strings.ContainsAny(name, ":\n\x00") {
		return fmt.Errorf("%w: %q", ErrInvalidStoreName, name)
	}

	return nil
}
