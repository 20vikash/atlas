package vm

import (
	"context"
	"sync"
)

// keyedLock is one identifier's lock. A buffered channel carries the single
// permit, so a waiter can give up when its context ends.
type keyedLock struct {
	available  chan struct{}
	references int
}

// keyedLocks serializes operations per virtual machine identifier. Entries exist
// only while an operation holds or waits for one, so an idle host holds no locks.
type keyedLocks struct {
	mutex sync.Mutex
	locks map[string]*keyedLock
}

// lock blocks until identifier is free and returns the function that frees it.
func (locks *keyedLocks) lock(ctx context.Context, identifier string) (func(), error) {
	entry := locks.reference(identifier)

	select {
	case <-entry.available:
		return func() {
			entry.available <- struct{}{}
			locks.release(identifier, entry)
		}, nil
	case <-ctx.Done():
		locks.release(identifier, entry)
		return nil, ctx.Err()
	}
}

// reference returns the entry for identifier and counts one more user of it.
func (locks *keyedLocks) reference(identifier string) *keyedLock {
	locks.mutex.Lock()
	defer locks.mutex.Unlock()

	if locks.locks == nil {
		locks.locks = make(map[string]*keyedLock)
	}

	entry := locks.locks[identifier]
	if entry == nil {
		entry = &keyedLock{available: make(chan struct{}, 1)}
		entry.available <- struct{}{}
		locks.locks[identifier] = entry
	}
	entry.references++

	return entry
}

// release drops one user of an entry and forgets the entry when none remain.
func (locks *keyedLocks) release(identifier string, entry *keyedLock) {
	locks.mutex.Lock()
	defer locks.mutex.Unlock()

	entry.references--
	if entry.references == 0 {
		delete(locks.locks, identifier)
	}
}
