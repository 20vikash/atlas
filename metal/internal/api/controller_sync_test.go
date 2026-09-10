package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/frappe/atlas/metal/internal/token"
)

// syncBody returns one complete sync request. A jwt fragment is added as given.
func syncBody(t *testing.T, jwt string) string {
	t.Helper()

	body := `{"wireguard_peers":[],"images":[],"privileged_vm_addresses":[]`
	if jwt != "" {
		body += `,"jwt":` + jwt
	}

	return body + `}`
}

// keysFragment renders trust state in the shape the sync request carries.
func keysFragment(t *testing.T, keys token.TrustedKeys) string {
	t.Helper()

	encoded, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}

	return string(encoded)
}

func TestSyncStoresAtlasPublicKeys(t *testing.T) {
	signer := newAtlasSigner(t)
	server, store := newServerWithTrustedKeys(t, &fakeVirtualMachineManager{virtualMachines: map[string]*fakeVM{}}, newFakeRuntimeServices(), &fakeWireGuardManager{})

	do(t, server, http.MethodPost, "/v1/sync", syncBody(t, keysFragment(t, signer.trusted)), http.StatusOK)

	stored := store.Keys()
	if stored.Issuer != testIssuer || stored.Receiver != testReceiver {
		t.Fatalf("stored identity = %s and %s", stored.Issuer, stored.Receiver)
	}
	if len(stored.PublicKeys) != 1 || stored.PublicKeys[0] != signer.trusted.PublicKeys[0] {
		t.Fatalf("stored keys = %+v", stored.PublicKeys)
	}
}

func TestSyncWithoutKeysKeepsTheStoredKeys(t *testing.T) {
	signer := newAtlasSigner(t)
	server, store := newServerWithTrustedKeys(t, &fakeVirtualMachineManager{virtualMachines: map[string]*fakeVM{}}, newFakeRuntimeServices(), &fakeWireGuardManager{})

	do(t, server, http.MethodPost, "/v1/sync", syncBody(t, keysFragment(t, signer.trusted)), http.StatusOK)
	do(t, server, http.MethodPost, "/v1/sync", syncBody(t, ""), http.StatusOK)

	if stored := store.Keys(); len(stored.PublicKeys) != 1 || stored.PublicKeys[0] != signer.trusted.PublicKeys[0] {
		t.Fatalf("stored keys = %+v", stored.PublicKeys)
	}
}

// Atlas repeats the current keys on every sync and adds the previous key while
// it rotates, so both requests must succeed.
func TestSyncAcceptsARepeatAndARotation(t *testing.T) {
	signer := newAtlasSigner(t)
	rotated := newAtlasSigner(t)
	server, store := newServerWithTrustedKeys(t, &fakeVirtualMachineManager{virtualMachines: map[string]*fakeVM{}}, newFakeRuntimeServices(), &fakeWireGuardManager{})

	first := keysFragment(t, signer.trusted)
	do(t, server, http.MethodPost, "/v1/sync", syncBody(t, first), http.StatusOK)
	do(t, server, http.MethodPost, "/v1/sync", syncBody(t, first), http.StatusOK)

	both := signer.trusted
	both.PublicKeys = []token.PublicKey{{ID: "key-2", Key: rotated.trusted.PublicKeys[0].Key}, signer.trusted.PublicKeys[0]}
	do(t, server, http.MethodPost, "/v1/sync", syncBody(t, keysFragment(t, both)), http.StatusOK)

	if stored := store.Keys(); len(stored.PublicKeys) != 2 {
		t.Fatalf("expected both keys, got %+v", stored.PublicKeys)
	}
}

func TestSyncRejectsKeysItCannotUse(t *testing.T) {
	server := newTestServer(t)

	recorder := do(
		t, server, http.MethodPost, "/v1/sync",
		syncBody(t, `{"issuer":"atlas-1","receiver":"node-fra-00001","public_keys":[{"id":"key-1","key":"not base64"}]}`),
		http.StatusBadRequest,
	)

	var response errorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "invalid_request" {
		t.Fatalf("error = %+v", response.Error)
	}
}

func TestSyncRejectsAChangedIssuerOrReceiver(t *testing.T) {
	signer := newAtlasSigner(t)
	server, store := newServerWithTrustedKeys(t, &fakeVirtualMachineManager{virtualMachines: map[string]*fakeVM{}}, newFakeRuntimeServices(), &fakeWireGuardManager{})

	do(t, server, http.MethodPost, "/v1/sync", syncBody(t, keysFragment(t, signer.trusted)), http.StatusOK)

	changed := signer.trusted
	changed.Receiver = "node-fra-00009"
	recorder := do(t, server, http.MethodPost, "/v1/sync", syncBody(t, keysFragment(t, changed)), http.StatusConflict)

	var response errorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "conflict" {
		t.Fatalf("error = %+v", response.Error)
	}
	if stored := store.Keys(); stored.Receiver != testReceiver {
		t.Fatalf("expected the first receiver, got %s", stored.Receiver)
	}
}
