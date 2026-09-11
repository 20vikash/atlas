package token

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newPublicKey(t *testing.T, id string) PublicKey {
	t.Helper()

	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return PublicKey{ID: id, Key: base64.RawURLEncoding.EncodeToString(publicKey)}
}

func newTrustedKeys(t *testing.T, ids ...string) TrustedKeys {
	t.Helper()

	keys := TrustedKeys{Issuer: "atlas-1", Receiver: "node-fra-00001"}
	for _, id := range ids {
		keys.PublicKeys = append(keys.PublicKeys, newPublicKey(t, id))
	}

	return keys
}

func TestNewKeyStoreWithoutFileIsEmpty(t *testing.T) {
	store, err := NewKeyStore(filepath.Join(t.TempDir(), "atlas-jwt.json"))
	if err != nil {
		t.Fatalf("new key store: %v", err)
	}

	if keys := store.Keys(); keys.Issuer != "" || len(keys.PublicKeys) != 0 {
		t.Fatalf("expected empty trust state, got %+v", keys)
	}
}

func TestNewKeyStoreLoadsStoredKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "atlas-jwt.json")
	store, err := NewKeyStore(path)
	if err != nil {
		t.Fatalf("new key store: %v", err)
	}

	written := newTrustedKeys(t, "key-1", "key-2")
	if err := store.Replace(written); err != nil {
		t.Fatalf("replace: %v", err)
	}

	reopened, err := NewKeyStore(path)
	if err != nil {
		t.Fatalf("reopen key store: %v", err)
	}

	loaded := reopened.Keys()
	if loaded.Issuer != written.Issuer || loaded.Receiver != written.Receiver {
		t.Fatalf("expected issuer %s and receiver %s, got %+v", written.Issuer, written.Receiver, loaded)
	}
	if len(loaded.PublicKeys) != 2 || loaded.PublicKeys[0] != written.PublicKeys[0] || loaded.PublicKeys[1] != written.PublicKeys[1] {
		t.Fatalf("expected the written keys, got %+v", loaded.PublicKeys)
	}
}

func TestNewKeyStoreRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "atlas-jwt.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o640); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := NewKeyStore(path); !errors.Is(err, ErrInvalidKeys) {
		t.Fatalf("expected ErrInvalidKeys, got %v", err)
	}
}

func TestNewKeyStoreRejectsStoredKeysItCannotUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "atlas-jwt.json")
	if err := os.WriteFile(path, []byte(`{"issuer":"","receiver":"node-fra-00001","public_keys":[]}`), 0o640); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := NewKeyStore(path); !errors.Is(err, ErrInvalidKeys) {
		t.Fatalf("expected ErrInvalidKeys, got %v", err)
	}
}

func TestReplaceRejectsKeysItCannotUse(t *testing.T) {
	valid := newTrustedKeys(t, "key-1")
	repeated := newTrustedKeys(t, "key-1")
	repeated.PublicKeys = append(repeated.PublicKeys, repeated.PublicKeys[0])

	cases := map[string]TrustedKeys{
		"no issuer":         {Receiver: valid.Receiver, PublicKeys: valid.PublicKeys},
		"no receiver":       {Issuer: valid.Issuer, PublicKeys: valid.PublicKeys},
		"no public key":     {Issuer: valid.Issuer, Receiver: valid.Receiver},
		"repeated key id":   repeated,
		"no key id":         {Issuer: valid.Issuer, Receiver: valid.Receiver, PublicKeys: []PublicKey{{Key: valid.PublicKeys[0].Key}}},
		"key is not base64": {Issuer: valid.Issuer, Receiver: valid.Receiver, PublicKeys: []PublicKey{{ID: "key-1", Key: "not base64"}}},
		"key is too short":  {Issuer: valid.Issuer, Receiver: valid.Receiver, PublicKeys: []PublicKey{{ID: "key-1", Key: base64.RawURLEncoding.EncodeToString([]byte("short"))}}},
	}

	for name, update := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "atlas-jwt.json")
			store, err := NewKeyStore(path)
			if err != nil {
				t.Fatalf("new key store: %v", err)
			}

			if err := store.Replace(update); !errors.Is(err, ErrInvalidKeys) {
				t.Fatalf("expected ErrInvalidKeys, got %v", err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected no file, got %v", err)
			}
		})
	}
}

func TestReplaceRejectsAChangedIssuerOrReceiver(t *testing.T) {
	store, err := NewKeyStore(filepath.Join(t.TempDir(), "atlas-jwt.json"))
	if err != nil {
		t.Fatalf("new key store: %v", err)
	}

	first := newTrustedKeys(t, "key-1")
	if err := store.Replace(first); err != nil {
		t.Fatalf("replace: %v", err)
	}

	changedIssuer := newTrustedKeys(t, "key-2")
	changedIssuer.Issuer = "atlas-2"
	if err := store.Replace(changedIssuer); !errors.Is(err, ErrKeyConflict) {
		t.Fatalf("expected ErrKeyConflict for a changed issuer, got %v", err)
	}

	changedReceiver := newTrustedKeys(t, "key-2")
	changedReceiver.Receiver = "node-fra-00002"
	if err := store.Replace(changedReceiver); !errors.Is(err, ErrKeyConflict) {
		t.Fatalf("expected ErrKeyConflict for a changed receiver, got %v", err)
	}

	if kept := store.Keys(); kept.PublicKeys[0] != first.PublicKeys[0] {
		t.Fatalf("expected the first keys, got %+v", kept.PublicKeys)
	}
}

func TestReplaceIsSafeToRepeat(t *testing.T) {
	store, err := NewKeyStore(filepath.Join(t.TempDir(), "atlas-jwt.json"))
	if err != nil {
		t.Fatalf("new key store: %v", err)
	}

	keys := newTrustedKeys(t, "key-1")
	for range 2 {
		if err := store.Replace(keys); err != nil {
			t.Fatalf("replace: %v", err)
		}
	}

	if stored := store.Keys(); len(stored.PublicKeys) != 1 || stored.PublicKeys[0] != keys.PublicKeys[0] {
		t.Fatalf("expected one unchanged key, got %+v", stored.PublicKeys)
	}
}

func TestReplaceKeepsKeysWhenTheWriteFails(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state", "atlas-jwt.json")
	store, err := NewKeyStore(path)
	if err != nil {
		t.Fatalf("new key store: %v", err)
	}

	first := newTrustedKeys(t, "key-1")
	if err := store.Replace(first); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// A file parent makes the next write fail.
	if err := os.RemoveAll(filepath.Join(directory, "state")); err != nil {
		t.Fatalf("remove directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "state"), nil, 0o640); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := store.Replace(newTrustedKeys(t, "key-2")); err == nil {
		t.Fatal("expected the replace to fail")
	}
	if kept := store.Keys(); len(kept.PublicKeys) != 1 || kept.PublicKeys[0] != first.PublicKeys[0] {
		t.Fatalf("expected the first keys, got %+v", kept.PublicKeys)
	}
}

func TestKeysDoNotSharePublicKeysWithTheStore(t *testing.T) {
	store, err := NewKeyStore(filepath.Join(t.TempDir(), "atlas-jwt.json"))
	if err != nil {
		t.Fatalf("new key store: %v", err)
	}

	keys := newTrustedKeys(t, "key-1")
	if err := store.Replace(keys); err != nil {
		t.Fatalf("replace: %v", err)
	}

	store.Keys().PublicKeys[0] = PublicKey{ID: "changed"}

	if stored := store.Keys(); stored.PublicKeys[0] != keys.PublicKeys[0] {
		t.Fatalf("expected the stored key to stay unchanged, got %+v", stored.PublicKeys[0])
	}
}
