// Package token stores host trust state and verifies Atlas tokens.
package token

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
)

var (
	// ErrInvalidKeys reports unusable trust state.
	ErrInvalidKeys = errors.New("invalid trusted keys")

	// ErrKeyConflict reports an issuer or receiver change.
	ErrKeyConflict = errors.New("trusted key identity conflict")
)

// PublicKey is an unpadded base64url Ed25519 public key.
type PublicKey struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

// TrustedKeys is one host's complete Atlas trust state.
type TrustedKeys struct {
	Issuer     string      `json:"issuer"`
	Receiver   string      `json:"receiver"`
	PublicKeys []PublicKey `json:"public_keys"`
}

// Validate rejects trust state that cannot verify a token.
func (keys TrustedKeys) Validate() error {
	if keys.Issuer == "" {
		return fmt.Errorf("%w: issuer is required", ErrInvalidKeys)
	}
	if keys.Receiver == "" {
		return fmt.Errorf("%w: receiver is required", ErrInvalidKeys)
	}
	return validatePublicKeys(keys.PublicKeys)
}

// clone copies keys so callers cannot change stored state.
func (keys TrustedKeys) clone() TrustedKeys {
	keys.PublicKeys = slices.Clone(keys.PublicKeys)
	return keys
}

func validatePublicKeys(publicKeys []PublicKey) error {
	if len(publicKeys) == 0 {
		return fmt.Errorf("%w: at least one public key is required", ErrInvalidKeys)
	}

	seenIdentifiers := make(map[string]struct{}, len(publicKeys))
	for _, publicKey := range publicKeys {
		if publicKey.ID == "" {
			return fmt.Errorf("%w: public key id is required", ErrInvalidKeys)
		}
		if _, repeated := seenIdentifiers[publicKey.ID]; repeated {
			return fmt.Errorf("%w: public key id %s is repeated", ErrInvalidKeys, publicKey.ID)
		}
		seenIdentifiers[publicKey.ID] = struct{}{}

		if _, err := publicKey.parse(); err != nil {
			return err
		}
	}

	return nil
}

func (publicKey PublicKey) parse() (ed25519.PublicKey, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(publicKey.Key)
	if err != nil {
		return nil, fmt.Errorf("%w: public key %s is not base64url", ErrInvalidKeys, publicKey.ID)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: public key %s is not an Ed25519 public key", ErrInvalidKeys, publicKey.ID)
	}

	return ed25519.PublicKey(decoded), nil
}
