package network

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const testTargetNamespacePath = "/run/netns/metal-test"

// fakeNamespaceSyscalls drives inNamespace without touching a real namespace.
type fakeNamespaceSyscalls struct {
	openFileDescriptors map[string]int
	openErrors          map[string]error
	setErrors           []error
	setCalls            []int
	closes              []int
	lockCount           int
	unlockCount         int
}

func (fake *fakeNamespaceSyscalls) open(path string) (int, error) {
	if err := fake.openErrors[path]; err != nil {
		return 0, err
	}
	return fake.openFileDescriptors[path], nil
}

func (fake *fakeNamespaceSyscalls) setNamespace(fileDescriptor int) error {
	index := len(fake.setCalls)
	fake.setCalls = append(fake.setCalls, fileDescriptor)
	if index < len(fake.setErrors) {
		return fake.setErrors[index]
	}
	return nil
}

func (fake *fakeNamespaceSyscalls) close(fileDescriptor int) error {
	fake.closes = append(fake.closes, fileDescriptor)
	return nil
}

func (fake *fakeNamespaceSyscalls) lockThread()   { fake.lockCount++ }
func (fake *fakeNamespaceSyscalls) unlockThread() { fake.unlockCount++ }

func newFakeNamespaceSyscalls() *fakeNamespaceSyscalls {
	return &fakeNamespaceSyscalls{
		openFileDescriptors: map[string]int{currentNamespacePath: 10, testTargetNamespacePath: 20},
		openErrors:          map[string]error{},
	}
}

func TestInNamespaceEntersRestoresAndUnlocks(t *testing.T) {
	fake := newFakeNamespaceSyscalls()
	callbackRan := false

	err := inNamespace(fake, testTargetNamespacePath, func() error {
		callbackRan = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if !callbackRan {
		t.Error("callback did not run")
	}
	// Enter the target with fd 20, then restore the current with fd 10.
	if want := []int{20, 10}; !slices.Equal(fake.setCalls, want) {
		t.Errorf("setNamespace calls = %v, want %v", fake.setCalls, want)
	}
	if fake.lockCount != 1 || fake.unlockCount != 1 {
		t.Errorf("lock/unlock = %d/%d, want 1/1", fake.lockCount, fake.unlockCount)
	}
	if want := []int{20, 10}; !slices.Equal(fake.closes, want) {
		t.Errorf("closed descriptors = %v, want %v", fake.closes, want)
	}
}

func TestInNamespaceReturnsTheCallbackError(t *testing.T) {
	fake := newFakeNamespaceSyscalls()
	wantErr := errors.New("attach failed")

	err := inNamespace(fake, testTargetNamespacePath, func() error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want the callback error", err)
	}
	if fake.unlockCount != 1 {
		t.Error("the thread must unlock after a normal restore")
	}
}

func TestInNamespaceKeepsTheThreadLockedWhenRestoreFails(t *testing.T) {
	fake := newFakeNamespaceSyscalls()
	restoreErr := errors.New("restore failed")
	fake.setErrors = []error{nil, restoreErr}
	callbackErr := errors.New("attach failed")

	err := inNamespace(fake, testTargetNamespacePath, func() error { return callbackErr })
	if !errors.Is(err, restoreErr) || !errors.Is(err, callbackErr) {
		t.Fatalf("error = %v, want both the callback and restore errors", err)
	}
	if fake.unlockCount != 0 {
		t.Error("the thread must stay locked when restore fails")
	}
}

func TestInNamespaceDoesNotRunTheCallbackWhenEntryFails(t *testing.T) {
	fake := newFakeNamespaceSyscalls()
	fake.setErrors = []error{errors.New("no such namespace")}
	callbackRan := false

	err := inNamespace(fake, testTargetNamespacePath, func() error {
		callbackRan = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "enter network namespace") {
		t.Fatalf("error = %v, want an enter error", err)
	}
	if callbackRan {
		t.Error("the callback must not run when entry fails")
	}
	if fake.unlockCount != 1 {
		t.Error("the thread must unlock because it never left the original namespace")
	}
}

func TestInNamespaceReportsAnOpenFailure(t *testing.T) {
	fake := newFakeNamespaceSyscalls()
	fake.openErrors[testTargetNamespacePath] = errors.New("missing")

	err := inNamespace(fake, testTargetNamespacePath, func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), testTargetNamespacePath) {
		t.Fatalf("error = %v, want the target path", err)
	}
	// The current descriptor opened first, so it must still close.
	if !slices.Contains(fake.closes, 10) {
		t.Error("the current namespace descriptor was not closed")
	}
	if fake.unlockCount != 1 {
		t.Error("the thread must unlock when it never entered the target")
	}
}
