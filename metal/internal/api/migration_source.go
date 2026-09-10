package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/frappe/atlas/metal/internal/vm"
)

// migrationSourceResponse is the portable state the source returns to the target.
type migrationSourceResponse struct {
	Config        vm.PortableConfig `json:"config"`
	ObservedState vm.State          `json:"observed_state"`
}

// prepareMigrationSource locks the source VM and returns its portable state. The
// token supplies the VM ID and caller, so the handler binds the claim to the VM.
func (s *Server) prepareMigrationSource(c echo.Context) error {
	identifier, err := migrationIdentifier(c)
	if err != nil {
		return err
	}
	claims := tokenClaims(c)
	handshake, err := s.migrationManager.LockSource(c.Request().Context(), identifier, claims.VirtualMachineID, claims.Caller)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, migrationSourceResponse{Config: handshake.Config, ObservedState: handshake.ObservedState})
}

// deleteMigrationSource unlocks the source VM and removes its migration state.
func (s *Server) deleteMigrationSource(c echo.Context) error {
	identifier, err := migrationIdentifier(c)
	if err != nil {
		return err
	}
	claims := tokenClaims(c)
	if err := s.migrationManager.UnlockSource(c.Request().Context(), identifier, claims.VirtualMachineID, claims.Caller); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
