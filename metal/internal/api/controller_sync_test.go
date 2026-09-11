package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSyncReturnsHostCapacity(t *testing.T) {
	server := newTestServer(t)

	recorder := do(t, server, http.MethodPost, "/v1/sync",
		`{"wireguard_peers":[],"images":[],"privileged_vm_addresses":[]}`, http.StatusOK)

	var response syncResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.VirtualMachines == nil {
		t.Fatalf("response = %+v", response)
	}
}

func TestSyncRejectsAMissingSet(t *testing.T) {
	server := newTestServer(t)

	cases := map[string]string{
		"wireguard_peers":         `{"images":[],"privileged_vm_addresses":[]}`,
		"images":                  `{"wireguard_peers":[],"privileged_vm_addresses":[]}`,
		"privileged_vm_addresses": `{"wireguard_peers":[],"images":[]}`,
	}
	for missing, body := range cases {
		t.Run(missing, func(t *testing.T) {
			do(t, server, http.MethodPost, "/v1/sync", body, http.StatusBadRequest)
		})
	}
}
