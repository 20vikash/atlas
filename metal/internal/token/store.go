package token

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	"github.com/frappe/atlas/metal/internal/platform"
)

// keyFileMode keeps the file out of reach of other host users.
const keyFileMode = 0o640

// KeyStore owns the trusted key file and the copy that the API reads.
type KeyStore struct {
	path    string
	mutex   sync.RWMutex
	trusted TrustedKeys
}

// NewKeyStore reads the trusted keys at path. A missing file leaves the store
// empty. A file that Metal cannot use returns an error.
func NewKeyStore(path string) (*KeyStore, error) {
	store := &KeyStore{path: path}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read trusted keys: %w", err)
	}

	var trusted TrustedKeys
	if err := json.Unmarshal(data, &trusted); err != nil {
		return nil, fmt.Errorf("%w: %s is not valid JSON", ErrInvalidKeys, path)
	}
	if err := trusted.Validate(); err != nil {
		return nil, err
	}
	store.trusted = trusted

	return store, nil
}

// Keys returns the current trust state.
func (store *KeyStore) Keys() TrustedKeys {
	store.mutex.RLock()
	defer store.mutex.RUnlock()

	return store.trusted.clone()
}

// Replace publishes the update and then swaps the copy in memory. Both stay
// unchanged when a step fails.
func (store *KeyStore) Replace(update TrustedKeys) error {
	if err := update.Validate(); err != nil {
		return err
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()

	if err := store.checkIdentity(update); err != nil {
		return err
	}

	data, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("encode trusted keys: %w", err)
	}
	if err := platform.WriteFile(store.path, data, keyFileMode); err != nil {
		return fmt.Errorf("write trusted keys: %w", err)
	}
	store.trusted = update.clone()

	return nil
}

// checkIdentity fixes the issuer and the receiver after the first update.
func (store *KeyStore) checkIdentity(update TrustedKeys) error {
	if store.trusted.Issuer == "" {
		return nil
	}
	if store.trusted.Issuer != update.Issuer || store.trusted.Receiver != update.Receiver {
		return fmt.Errorf("%w: issuer and receiver cannot change", ErrKeyConflict)
	}

	return nil
}
