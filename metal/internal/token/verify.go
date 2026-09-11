package token

import (
	"errors"
	"fmt"
	"slices"

	"github.com/golang-jwt/jwt/v5"
)

var (
	// ErrUnauthorized reports a missing, invalid, expired, or misaddressed token.
	ErrUnauthorized = errors.New("unauthorized token")

	// ErrForbidden reports a valid token missing a required scope.
	ErrForbidden = errors.New("forbidden token")
)

// Scope is one permission in an Atlas token.
type Scope string

const (
	// ScopeReadVirtualMachine permits reading the named VM.
	ScopeReadVirtualMachine Scope = "read_vm"

	// ScopeMigration permits migration operations on the named VM.
	ScopeMigration Scope = "migration"
)

// Claims is the validated content of an Atlas token.
type Claims struct {
	VirtualMachineID string
	Caller           string
	Scopes           []Scope
}

// tokenClaims is the wire form the parser fills in.
type tokenClaims struct {
	VirtualMachineID string   `json:"vm_id"`
	Scopes           []string `json:"scope"`
	jwt.RegisteredClaims
}

// Verify checks an Atlas token and returns its claims.
func Verify(keys TrustedKeys, signedToken string, requiredScopes ...Scope) (Claims, error) {
	if len(keys.PublicKeys) == 0 {
		return Claims{}, fmt.Errorf("%w: the host trusts no Atlas key", ErrUnauthorized)
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithIssuer(keys.Issuer),
		jwt.WithAudience(keys.Receiver),
		jwt.WithExpirationRequired(),
	)

	var parsed tokenClaims
	if _, err := parser.ParseWithClaims(signedToken, &parsed, keys.signingKey); err != nil {
		return Claims{}, fmt.Errorf("%w: %s", ErrUnauthorized, err)
	}

	claims, err := parsed.claims()
	if err != nil {
		return Claims{}, err
	}

	if err := claims.requireScopes(requiredScopes); err != nil {
		return Claims{}, err
	}

	return claims, nil
}

func (keys TrustedKeys) signingKey(token *jwt.Token) (any, error) {
	identifier, _ := token.Header["kid"].(string)
	if identifier == "" {
		return nil, errors.New("token has no key id")
	}

	for _, publicKey := range keys.PublicKeys {
		if publicKey.ID == identifier {
			return publicKey.parse()
		}
	}

	return nil, fmt.Errorf("key id %s is not trusted", identifier)
}

func (parsed tokenClaims) claims() (Claims, error) {
	if parsed.VirtualMachineID == "" {
		return Claims{}, fmt.Errorf("%w: token has no vm_id", ErrUnauthorized)
	}
	if parsed.Subject == "" {
		return Claims{}, fmt.Errorf("%w: token has no caller", ErrUnauthorized)
	}
	if len(parsed.Scopes) == 0 {
		return Claims{}, fmt.Errorf("%w: token has no scope", ErrUnauthorized)
	}

	scopes := make([]Scope, 0, len(parsed.Scopes))
	for _, name := range parsed.Scopes {
		scope := Scope(name)
		if !scope.isKnown() {
			return Claims{}, fmt.Errorf("%w: scope %s is not valid", ErrUnauthorized, name)
		}
		scopes = append(scopes, scope)
	}

	return Claims{VirtualMachineID: parsed.VirtualMachineID, Caller: parsed.Subject, Scopes: scopes}, nil
}

func (claims Claims) requireScopes(required []Scope) error {
	for _, scope := range required {
		if !slices.Contains(claims.Scopes, scope) {
			return fmt.Errorf("%w: scope %s is required", ErrForbidden, scope)
		}
	}

	return nil
}

func (scope Scope) isKnown() bool {
	return scope == ScopeReadVirtualMachine || scope == ScopeMigration
}
