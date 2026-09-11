package token

import (
	"os"
	"strings"
	"testing"
)

// TestAtlasIssuedTokenIsAccepted verifies the token created by the check script.
func TestAtlasIssuedTokenIsAccepted(t *testing.T) {
	keysFile := os.Getenv("ATLAS_TRUSTED_KEYS_FILE")
	tokenFile := os.Getenv("ATLAS_TOKEN_FILE")
	if keysFile == "" || tokenFile == "" {
		t.Skip("run scripts/check-metal-token.sh to check a token that Atlas signed")
	}

	store, err := NewKeyStore(keysFile)
	if err != nil {
		t.Fatalf("load the Atlas trust state: %v", err)
	}

	signedToken, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("read the Atlas token: %v", err)
	}

	claims, err := Verify(store.Keys(), strings.TrimSpace(string(signedToken)), ScopeReadVirtualMachine, ScopeMigration)
	if err != nil {
		t.Fatalf("verify the Atlas token: %v", err)
	}

	t.Logf("accepted vm %s from caller %s with scopes %v", claims.VirtualMachineID, claims.Caller, claims.Scopes)
}
