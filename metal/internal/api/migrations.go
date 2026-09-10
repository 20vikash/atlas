package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/frappe/atlas/metal/internal/vm"
)

// errInvalidMigrationRequest reports a target request that is missing a field.
var errInvalidMigrationRequest = errors.New("virtual_machine_id, source, and token are required")

// createMigrationRequest is the target-pull request that Atlas sends the target.
type createMigrationRequest struct {
	VirtualMachineID string `json:"virtual_machine_id"`
	Source           string `json:"source"`
	Token            string `json:"token"`
}

// validate rejects a request that is missing a required field.
func (r createMigrationRequest) validate() error {
	if r.VirtualMachineID == "" || r.Source == "" || r.Token == "" {
		return errInvalidMigrationRequest
	}
	return nil
}

// migrationResponse is the public view of one migration.
type migrationResponse struct {
	ID               string                  `json:"id"`
	VirtualMachineID string                  `json:"virtual_machine_id"`
	Status           string                  `json:"status"`
	Phase            string                  `json:"phase"`
	Error            *migrationErrorResponse `json:"error,omitempty"`
}

// migrationErrorResponse is the safe error detail of one migration.
type migrationErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// createMigration creates or resumes a target migration and starts the worker.
func (s *Server) createMigration(c echo.Context) error {
	identifier, err := migrationIdentifier(c)
	if err != nil {
		return err
	}
	var request createMigrationRequest
	if err := decodeJSONRequest(c, &request); err != nil {
		return err
	}
	if err := request.validate(); err != nil {
		return badRequest(err.Error())
	}

	record, err := s.migrationManager.CreateTarget(c.Request().Context(), identifier, request.VirtualMachineID, request.Source, request.Token)
	if err != nil {
		return err
	}
	s.wakeReconciler()
	return c.JSON(http.StatusAccepted, toMigration(record))
}

// getMigration returns the status of one target migration.
func (s *Server) getMigration(c echo.Context) error {
	identifier, err := migrationIdentifier(c)
	if err != nil {
		return err
	}
	record, err := s.migrationManager.TargetStatus(c.Request().Context(), identifier)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toMigration(record))
}

// abortMigration removes a target migration that has not finished.
func (s *Server) abortMigration(c echo.Context) error {
	identifier, err := migrationIdentifier(c)
	if err != nil {
		return err
	}
	if err := s.migrationManager.AbortTarget(c.Request().Context(), identifier); err != nil {
		return err
	}
	return c.NoContent(http.StatusAccepted)
}

// toMigration maps a target record to its public response.
func toMigration(record vm.TargetMigrationRecord) migrationResponse {
	response := migrationResponse{
		ID:               record.ID,
		VirtualMachineID: record.VirtualMachineID,
		Status:           string(record.Status),
		Phase:            string(record.Phase),
	}
	if record.Error != nil {
		response.Error = &migrationErrorResponse{Code: record.Error.Code, Message: record.Error.Message}
	}
	return response
}

// migrationIdentifier reads and validates the migration ID in the request path.
func migrationIdentifier(c echo.Context) (string, error) {
	identifier := c.Param("id")
	if !validResourceID(identifier) {
		return "", badRequest("invalid migration identifier")
	}
	return identifier, nil
}
