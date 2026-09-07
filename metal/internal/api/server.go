// Package api serves the Metal HTTP API.
package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/frappe/atlas/metal/internal/console"
	"github.com/frappe/atlas/metal/internal/host"
	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/vm"
)

// Config contains HTTP server configuration. Only the token hash is stored, so
// the plain token never reaches this package.
type Config struct {
	AuthTokenHash string
	Logger        *slog.Logger
}

// SnapshotStore uploads and removes local image staging snapshots.
type SnapshotStore interface {
	StartUpload(ctx context.Context, snapshotID string, request storage.SnapshotUploadRequest) error
	UploadStatus(ctx context.Context, snapshotID string) (storage.SnapshotUploadStatus, error)
	DeleteSnapshot(ctx context.Context, snapshotID string) error
}

// HostService synchronizes controller-owned host state and reports capacity.
type HostService interface {
	Synchronize(context.Context, host.DesiredState) (host.Capacity, error)
	Capacity(context.Context) (host.Capacity, error)
}

// SerialBroker streams a virtual machine serial console to one viewer.
type SerialBroker interface {
	Attach(ctx context.Context, id string, client io.ReadWriter, resize <-chan console.Winsize) error
}

// VirtualMachineManager owns virtual machine state and operations.
type VirtualMachineManager interface {
	Create(context.Context, string, vm.Specification) (vm.Information, error)
	Information(context.Context, string) (vm.Information, error)
	List(context.Context) ([]vm.Information, error)
	SetPowerState(context.Context, string, vm.State) error
	RequestRestart(context.Context, string) error
	SetCompute(context.Context, string, int, int) error
	SetDisk(context.Context, string, int, vm.Disk) error
	SetNetwork(context.Context, string, vm.NetworkConfiguration) error
	SetSleepPolicy(context.Context, string, bool) error
	ReplaceSSHKeys(context.Context, string, []string) (bool, error)
	ReplaceMetadata(context.Context, string, map[string]string) (bool, error)
	Delete(context.Context, string) error
	CreateSnapshot(context.Context, string) (vm.StagedSnapshot, error)
	ConnectSSH(context.Context, string) (vm.SSHConnection, error)
}

// Dependencies contains services used by the HTTP handlers.
type Dependencies struct {
	VirtualMachineManager VirtualMachineManager
	SnapshotStore         SnapshotStore
	WakeReconciler        func()
	HostService           HostService
	SerialBroker          SerialBroker
}

// Server owns the HTTP handlers and their dependencies.
type Server struct {
	virtualMachineManager VirtualMachineManager
	snapshotStore         SnapshotStore
	wakeReconciler        func()
	hostService           HostService
	serialBroker          SerialBroker
	authTokenHash         []byte
	logger                *slog.Logger
}

// New builds the HTTP router from explicit configuration and dependencies.
func New(configuration Config, dependencies Dependencies) (*echo.Echo, error) {
	if err := validateServerConfiguration(configuration, dependencies); err != nil {
		return nil, err
	}

	server := &Server{
		virtualMachineManager: dependencies.VirtualMachineManager,
		snapshotStore:         dependencies.SnapshotStore,
		wakeReconciler:        dependencies.WakeReconciler,
		hostService:           dependencies.HostService,
		serialBroker:          dependencies.SerialBroker,
		authTokenHash:         []byte(configuration.AuthTokenHash),
	}
	if configuration.Logger == nil {
		configuration.Logger = slog.Default()
	}
	server.logger = configuration.Logger

	router := echo.New()
	router.HideBanner = true
	router.HTTPErrorHandler = errorHandler

	// Correlation runs first, so every log line and error carries the same IDs.
	router.Use(correlationMiddleware)
	router.Use(server.logRequest)
	router.Use(server.authenticate)
	server.registerRoutes(router)

	return router, nil
}

// logRequest records the outcome of every request. The status of a failed
// request comes from the public error, because the handler returned before the
// response was written.
func (s *Server) logRequest(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Set("logger", s.logger)

		err := next(c)
		status := c.Response().Status
		if err != nil {
			status = publicAPIError(err).status
		}

		s.logger.Info("API request",
			"method", c.Request().Method, "path", c.Path(), "status", status,
			"request_id", requestID(c.Request().Context()),
			"operation_id", operationID(c.Request().Context()),
		)

		return err
	}
}

// validateServerConfiguration rejects a server that cannot serve safely. The
// token hash is required, so an unset token can never mean an open API.
func validateServerConfiguration(configuration Config, dependencies Dependencies) error {
	if len(configuration.AuthTokenHash) != sha256.Size*2 {
		return fmt.Errorf("API authentication token SHA-256 hash is required")
	}
	if _, err := hex.DecodeString(configuration.AuthTokenHash); err != nil || configuration.AuthTokenHash != strings.ToLower(configuration.AuthTokenHash) {
		return fmt.Errorf("API authentication token SHA-256 hash is invalid")
	}
	if dependencies.VirtualMachineManager == nil || dependencies.SnapshotStore == nil || dependencies.WakeReconciler == nil || dependencies.HostService == nil || dependencies.SerialBroker == nil {
		return fmt.Errorf("API dependencies are required")
	}
	return nil
}

// authenticate accepts a bearer token whose SHA-256 digest matches the
// configured hash. The comparison is constant time, so a wrong token reveals
// nothing through timing.
func (s *Server) authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if isPublicPath(c.Path()) {
			return next(c)
		}

		token := c.Request().Header.Get("Authorization")
		const prefix = "Bearer "
		if len(token) <= len(prefix) || token[:len(prefix)] != prefix {
			return unauthorized()
		}

		digest := sha256.Sum256([]byte(token[len(prefix):]))
		if subtle.ConstantTimeCompare(s.authTokenHash, []byte(hex.EncodeToString(digest[:]))) != 1 {
			return unauthorized()
		}
		return next(c)
	}
}

// isPublicPath reports whether a route serves without a token. Liveness must
// answer a probe that holds no token, and the documentation page is opened in a
// browser that cannot send one. Neither carries VM data, so both stay open and
// metald is expected to listen on a private control network.
func isPublicPath(path string) bool {
	return path == "/health" || path == "/docs" || path == "/docs/swagger.json"
}
