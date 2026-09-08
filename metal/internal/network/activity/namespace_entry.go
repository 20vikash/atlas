package activity

import (
	"errors"
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

// currentNamespacePath follows the calling thread after Setns.
const currentNamespacePath = "/proc/thread-self/ns/net"

// namespaceSyscalls enters and leaves a network namespace.
type namespaceSyscalls interface {
	open(path string) (int, error)
	setNamespace(fileDescriptor int) error
	close(fileDescriptor int) error
	lockThread()
	unlockThread()
}

// inNamespace runs callback in a network namespace. If restore fails, the
// thread stays locked so Go cannot reuse it in the wrong namespace.
func inNamespace(syscalls namespaceSyscalls, path string, callback func() error) error {
	syscalls.lockThread()
	safeToReuseThread := true
	defer func() {
		if safeToReuseThread {
			syscalls.unlockThread()
		}
	}()

	currentDescriptor, err := syscalls.open(currentNamespacePath)
	if err != nil {
		return fmt.Errorf("open current network namespace: %w", err)
	}
	defer syscalls.close(currentDescriptor)

	targetDescriptor, err := syscalls.open(path)
	if err != nil {
		return fmt.Errorf("open network namespace %s: %w", path, err)
	}
	defer syscalls.close(targetDescriptor)

	if err := syscalls.setNamespace(targetDescriptor); err != nil {
		return fmt.Errorf("enter network namespace %s: %w", path, err)
	}
	safeToReuseThread = false

	callbackError := callback()

	if restoreError := syscalls.setNamespace(currentDescriptor); restoreError != nil {
		return errors.Join(callbackError, fmt.Errorf("restore network namespace: %w", restoreError))
	}
	safeToReuseThread = true
	return callbackError
}

// osNamespaceSyscalls changes real network namespaces.
type osNamespaceSyscalls struct{}

func (osNamespaceSyscalls) open(path string) (int, error) {
	return unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
}

func (osNamespaceSyscalls) setNamespace(fileDescriptor int) error {
	return unix.Setns(fileDescriptor, unix.CLONE_NEWNET)
}

func (osNamespaceSyscalls) close(fileDescriptor int) error { return unix.Close(fileDescriptor) }

func (osNamespaceSyscalls) lockThread() { runtime.LockOSThread() }

func (osNamespaceSyscalls) unlockThread() { runtime.UnlockOSThread() }
