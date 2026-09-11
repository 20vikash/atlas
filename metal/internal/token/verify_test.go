package token

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "atlas-1"
	testReceiver = "node-fra-00001"
	testCaller   = "node-fra-00002"
)

// atlasKey is an Atlas key pair for test signing.
type atlasKey struct {
	identifier string
	private    ed25519.PrivateKey
	public     PublicKey
}

func newAtlasKey(t *testing.T, identifier string) atlasKey {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return atlasKey{
		identifier: identifier,
		private:    privateKey,
		public:     PublicKey{ID: identifier, Key: base64.RawURLEncoding.EncodeToString(publicKey)},
	}
}

func (key atlasKey) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = key.identifier

	signed, err := token.SignedString(key.private)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	return signed
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"vm_id": "vm-00001",
		"scope": []string{string(ScopeReadVirtualMachine), string(ScopeMigration)},
		"iss":   testIssuer,
		"sub":   testCaller,
		"aud":   testReceiver,
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
}

func trust(keys ...atlasKey) TrustedKeys {
	trusted := TrustedKeys{Issuer: testIssuer, Receiver: testReceiver}
	for _, key := range keys {
		trusted.PublicKeys = append(trusted.PublicKeys, key.public)
	}

	return trusted
}

func TestVerifyAcceptsACorrectToken(t *testing.T) {
	key := newAtlasKey(t, "key-1")

	claims, err := Verify(trust(key), key.sign(t, validClaims()), ScopeReadVirtualMachine, ScopeMigration)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	if claims.VirtualMachineID != "vm-00001" {
		t.Fatalf("expected vm-00001, got %s", claims.VirtualMachineID)
	}
	if claims.Caller != testCaller {
		t.Fatalf("expected caller %s, got %s", testCaller, claims.Caller)
	}
	if len(claims.Scopes) != 2 {
		t.Fatalf("expected both scopes, got %v", claims.Scopes)
	}
}

func TestVerifyAcceptsThePreviousKeyDuringRotation(t *testing.T) {
	current := newAtlasKey(t, "key-2")
	previous := newAtlasKey(t, "key-1")

	if _, err := Verify(trust(current, previous), previous.sign(t, validClaims()), ScopeMigration); err != nil {
		t.Fatalf("expected the previous key to stay usable, got %v", err)
	}
}

func TestVerifyRejectsChangedClaims(t *testing.T) {
	cases := map[string]func(jwt.MapClaims){
		"another issuer":   func(claims jwt.MapClaims) { claims["iss"] = "atlas-2" },
		"another receiver": func(claims jwt.MapClaims) { claims["aud"] = "node-fra-00009" },
		"expired":          func(claims jwt.MapClaims) { claims["exp"] = time.Now().Add(-time.Minute).Unix() },
		"no expiry":        func(claims jwt.MapClaims) { delete(claims, "exp") },
		"no issuer":        func(claims jwt.MapClaims) { delete(claims, "iss") },
		"no receiver":      func(claims jwt.MapClaims) { delete(claims, "aud") },
		"no vm id":         func(claims jwt.MapClaims) { delete(claims, "vm_id") },
		"no caller":        func(claims jwt.MapClaims) { delete(claims, "sub") },
		"no scope":         func(claims jwt.MapClaims) { delete(claims, "scope") },
		"an unknown scope": func(claims jwt.MapClaims) { claims["scope"] = []string{"read_vm", "host_admin"} },
	}

	key := newAtlasKey(t, "key-1")
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			claims := validClaims()
			change(claims)

			if _, err := Verify(trust(key), key.sign(t, claims), ScopeReadVirtualMachine); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("expected ErrUnauthorized, got %v", err)
			}
		})
	}
}

func TestVerifyRejectsAnUntrustedSignature(t *testing.T) {
	trusted := newAtlasKey(t, "key-1")
	attacker := newAtlasKey(t, "key-1")

	if _, err := Verify(trust(trusted), attacker.sign(t, validClaims()), ScopeMigration); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestVerifyRejectsAnUnusableKeyID(t *testing.T) {
	key := newAtlasKey(t, "key-1")
	unknown := newAtlasKey(t, "key-9")

	if _, err := Verify(trust(key), unknown.sign(t, validClaims()), ScopeMigration); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for an unknown key id, got %v", err)
	}

	withoutIdentifier := jwt.NewWithClaims(jwt.SigningMethodEdDSA, validClaims())
	signed, err := withoutIdentifier.SignedString(key.private)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	if _, err := Verify(trust(key), signed, ScopeMigration); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for a missing key id, got %v", err)
	}
}

func TestVerifyRejectsAnotherSigningMethod(t *testing.T) {
	key := newAtlasKey(t, "key-1")

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims())
	token.Header["kid"] = key.identifier
	signed, err := token.SignedString([]byte("shared secret"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	if _, err := Verify(trust(key), signed, ScopeMigration); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestVerifyRejectsAMissingScope(t *testing.T) {
	key := newAtlasKey(t, "key-1")
	claims := validClaims()
	claims["scope"] = []string{string(ScopeReadVirtualMachine)}

	if _, err := Verify(trust(key), key.sign(t, claims), ScopeMigration); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestVerifyRejectsEveryTokenWhenTheHostTrustsNoKey(t *testing.T) {
	key := newAtlasKey(t, "key-1")

	empty := TrustedKeys{Issuer: testIssuer, Receiver: testReceiver}
	if _, err := Verify(empty, key.sign(t, validClaims()), ScopeMigration); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}
