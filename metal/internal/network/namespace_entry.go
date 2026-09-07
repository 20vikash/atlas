package network

import (
	"errors"
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

// currentNamespacePath is the network namespace of the running thread.
// /proc/thread-self points to the calling thread, not the main thread, so it is
// correct after runtime.LockOSThread and after a Setns on this thread.
const currentNamespacePath = "/proc/thread-self/ns/net"

// namespaceSyscalls are the operations that enter and leave a network
// namespace. A fake replaces them in unit tests, so the tests need no root.
type namespaceSyscalls interface {
	open(path string) (int, error)
	setNamespace(fileDescriptor int) error
	close(fileDescriptor int) error
	lockThread()
	unlockThread()
}

// inNamespace runs callback inside the network namespace at path. It locks the
// OS thread first, so no other goroutine runs in the wrong namespace. It
// restores the original namespace on every path where it entered the target.
//
// If restore fails, the thread stays locked and is never unlocked. The Go
// runtime then discards the thread instead of reusing it in the wrong
// namespace.
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

// osNamespaceSyscalls enters real network namespaces.
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
