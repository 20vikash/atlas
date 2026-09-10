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

// nextSnapshotRequest acknowledges the last received sequence.
type nextSnapshotRequest struct {
	ReceivedSequence int `json:"received_sequence"`
}

// snapshotResponse describes the next snapshot the target can pull.
type snapshotResponse struct {
	Sequence  int    `json:"sequence"`
	SizeBytes int64  `json:"size_bytes"`
	GUID      string `json:"guid"`
}

// streamSnapshotRequest names the snapshot stream the target wants. The optional
// throughput limit caps the source disk during this interval.
type streamSnapshotRequest struct {
	Sequence        int    `json:"sequence"`
	ResumeToken     string `json:"resume_token"`
	ThroughputMiBps int    `json:"throughput_mibps"`
}

// createMigrationSnapshot returns the next source snapshot for the target.
func (s *Server) createMigrationSnapshot(c echo.Context) error {
	identifier, err := migrationIdentifier(c)
	if err != nil {
		return err
	}
	var request nextSnapshotRequest
	if err := decodeOptionalJSON(c, &request); err != nil {
		return err
	}
	claims := tokenClaims(c)
	snapshot, err := s.migrationManager.NextSourceSnapshot(c.Request().Context(), identifier, claims.VirtualMachineID, claims.Caller, request.ReceivedSequence)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, snapshotResponse{Sequence: snapshot.Sequence, SizeBytes: snapshot.SizeBytes, GUID: snapshot.GUID})
}

// streamMigrationSnapshot streams one snapshot to the target. The handler writes
// no status until the first byte, so a validation error still maps to a code.
func (s *Server) streamMigrationSnapshot(c echo.Context) error {
	identifier, err := migrationIdentifier(c)
	if err != nil {
		return err
	}
	var request streamSnapshotRequest
	if err := decodeJSONRequest(c, &request); err != nil {
		return err
	}
	claims := tokenClaims(c)
	c.Response().Header().Set(echo.HeaderContentType, "application/octet-stream")
	_, err = s.migrationManager.SendSourceStream(c.Request().Context(), identifier, claims.VirtualMachineID, claims.Caller, request.Sequence, request.ResumeToken, request.ThroughputMiBps, c.Response())
	return err
}

// stopMigrationSource stops the source, removes its network, and returns the
// final snapshot for the target to pull.
func (s *Server) stopMigrationSource(c echo.Context) error {
	identifier, err := migrationIdentifier(c)
	if err != nil {
		return err
	}
	claims := tokenClaims(c)
	snapshot, err := s.migrationManager.StopSource(c.Request().Context(), identifier, claims.VirtualMachineID, claims.Caller)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, snapshotResponse{Sequence: snapshot.Sequence, SizeBytes: snapshot.SizeBytes, GUID: snapshot.GUID})
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
