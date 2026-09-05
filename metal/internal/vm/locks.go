package vm

import (
	"context"
	"sync"
)

type keyedLock struct {
	available  chan struct{}
	references int
}

type keyedLocks struct {
	mutex sync.Mutex
	locks map[string]*keyedLock
}

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

func (locks *keyedLocks) release(identifier string, entry *keyedLock) {
	locks.mutex.Lock()
	defer locks.mutex.Unlock()
	entry.references--
	if entry.references == 0 {
		delete(locks.locks, identifier)
	}
}
