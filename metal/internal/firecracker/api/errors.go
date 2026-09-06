package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Error is a non-2xx response from the Firecracker API. Callers inspect Status
// to tell a rejected request from an unreachable process.
type Error struct {
	Status  int
	Message string
}

// Error reports the status and the fault message Firecracker returned.
func (e *Error) Error() string {
	return fmt.Sprintf("firecracker api: %d: %s", e.Status, e.Message)
}

// decodeFault builds an Error from a failed response. A body that does not
// decode still yields the status, which is the part a caller acts on.
func decodeFault(response *http.Response) error {
	var fault struct {
		FaultMessage string `json:"fault_message"`
	}
	_ = json.NewDecoder(response.Body).Decode(&fault)

	return &Error{Status: response.StatusCode, Message: fault.FaultMessage}
}
