package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/frappe/atlas/metal/internal/vm"
)

// migrationSourceResponse is portable source state for the target.
type migrationSourceResponse struct {
	Config        vm.PortableConfig `json:"config"`
	ObservedState vm.State          `json:"observed_state"`
}

// prepareMigrationSource locks the source and returns portable state.
func (s *Server) prepareMigrationSource(c echo.Context) error {
	identifier, virtualMachineID, err := migrationSourceIdentifiers(c)
	if err != nil {
		return err
	}
	handshake, err := s.migrationManager.LockSource(c.Request().Context(), identifier, virtualMachineID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, migrationSourceResponse{Config: handshake.Config, ObservedState: handshake.ObservedState})
}

// nextSnapshotRequest acknowledges the last sequence.
type nextSnapshotRequest struct {
	ReceivedSequence int `json:"received_sequence"`
}

// snapshotResponse describes the next snapshot.
type snapshotResponse struct {
	Sequence  int    `json:"sequence"`
	SizeBytes int64  `json:"size_bytes"`
	GUID      string `json:"guid"`
}

// streamSnapshotRequest names a snapshot stream and optional disk limit.
type streamSnapshotRequest struct {
	Sequence        int    `json:"sequence"`
	ResumeToken     string `json:"resume_token"`
	ThroughputMiBps int    `json:"throughput_mibps"`
}

// createMigrationSnapshot returns the next source snapshot.
func (s *Server) createMigrationSnapshot(c echo.Context) error {
	identifier, virtualMachineID, err := migrationSourceIdentifiers(c)
	if err != nil {
		return err
	}
	var request nextSnapshotRequest
	if err := decodeOptionalJSON(c, &request); err != nil {
		return err
	}
	snapshot, err := s.migrationManager.NextSourceSnapshot(c.Request().Context(), identifier, virtualMachineID, request.ReceivedSequence)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, snapshotResponse{Sequence: snapshot.Sequence, SizeBytes: snapshot.SizeBytes, GUID: snapshot.GUID})
}

// streamMigrationSnapshot streams one snapshot. It writes no status until data.
func (s *Server) streamMigrationSnapshot(c echo.Context) error {
	identifier, virtualMachineID, err := migrationSourceIdentifiers(c)
	if err != nil {
		return err
	}
	var request streamSnapshotRequest
	if err := decodeJSONRequest(c, &request); err != nil {
		return err
	}
	c.Response().Header().Set(echo.HeaderContentType, "application/octet-stream")
	_, err = s.migrationManager.SendSourceStream(c.Request().Context(), identifier, virtualMachineID, request.Sequence, request.ResumeToken, request.ThroughputMiBps, c.Response())
	return err
}

// stopMigrationSource stops the source, removes its network, and returns its
// final snapshot.
func (s *Server) stopMigrationSource(c echo.Context) error {
	identifier, virtualMachineID, err := migrationSourceIdentifiers(c)
	if err != nil {
		return err
	}
	snapshot, err := s.migrationManager.StopSource(c.Request().Context(), identifier, virtualMachineID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, snapshotResponse{Sequence: snapshot.Sequence, SizeBytes: snapshot.SizeBytes, GUID: snapshot.GUID})
}

// startMigrationSource restores the source during rollback.
func (s *Server) startMigrationSource(c echo.Context) error {
	identifier, virtualMachineID, err := migrationSourceIdentifiers(c)
	if err != nil {
		return err
	}
	if err := s.migrationManager.StartSourceRollback(c.Request().Context(), identifier, virtualMachineID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// destroyMigrationSource destroys the stopped source and its migration state.
func (s *Server) destroyMigrationSource(c echo.Context) error {
	identifier, virtualMachineID, err := migrationSourceIdentifiers(c)
	if err != nil {
		return err
	}
	if err := s.migrationManager.DestroySource(c.Request().Context(), identifier, virtualMachineID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// deleteMigrationSource unlocks the source and removes migration state.
func (s *Server) deleteMigrationSource(c echo.Context) error {
	identifier, virtualMachineID, err := migrationSourceIdentifiers(c)
	if err != nil {
		return err
	}
	if err := s.migrationManager.UnlockSource(c.Request().Context(), identifier, virtualMachineID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// migrationSourceIdentifiers reads the migration ID and the VM ID the target
// sends. The target and source hold the same VM ID across a migration.
func migrationSourceIdentifiers(c echo.Context) (string, string, error) {
	identifier, err := migrationIdentifier(c)
	if err != nil {
		return "", "", err
	}
	virtualMachineID := c.QueryParam("virtual_machine_id")
	if !validResourceID(virtualMachineID) {
		return "", "", badRequest("invalid virtual_machine_id")
	}
	return identifier, virtualMachineID, nil
}
