package api

import (
	"github.com/labstack/echo/v4"

	"github.com/frappe/atlas/metal/internal/token"
)

// registerRoutes binds handlers. PUT mutations are idempotent; POST is for
// actions and creation of addressable resources. Health and documentation carry
// no VM data and stay open.
func (s *Server) registerRoutes(router *echo.Echo) {
	router.GET("/health", s.checkHealth)
	router.GET("/docs", s.showDocumentation)
	router.GET("/docs/swagger.json", s.getOpenAPISpecification)

	versionOneRoutes := router.Group("/v1", s.authenticate)
	versionOneRoutes.POST("/sync", s.exchangeControllerState)

	virtualMachineRoutes := versionOneRoutes.Group("/vms")
	virtualMachineRoutes.GET("", s.listVirtualMachines)
	virtualMachineRoutes.PUT("/:id", s.createVirtualMachine)
	virtualMachineRoutes.GET("/:id", s.getVirtualMachine)
	virtualMachineRoutes.PUT("/:id/power", s.setVirtualMachinePowerState)
	virtualMachineRoutes.POST("/:id/restart", s.restartVirtualMachine)
	virtualMachineRoutes.PUT("/:id/compute", s.setVirtualMachineCompute)
	virtualMachineRoutes.PUT("/:id/disk", s.setVirtualMachineDisk)
	virtualMachineRoutes.PUT("/:id/network", s.setVirtualMachineNetwork)
	virtualMachineRoutes.PUT("/:id/ssh-keys", s.replaceVirtualMachineSSHKeys)
	virtualMachineRoutes.PUT("/:id/metadata", s.replaceVirtualMachineMetadata)
	virtualMachineRoutes.DELETE("/:id", s.deleteVirtualMachine)
	virtualMachineRoutes.POST("/:id/snapshots", s.createVirtualMachineSnapshot)
	virtualMachineRoutes.GET("/:id/console", s.getVirtualMachineConsole)

	snapshotRoutes := versionOneRoutes.Group("/snapshots")
	snapshotRoutes.POST("/:id/upload", s.uploadSnapshot)
	snapshotRoutes.GET("/:id", s.getSnapshot)
	snapshotRoutes.DELETE("/:id", s.deleteSnapshot)

	// Atlas drives these with the static Metal token.
	migrationRoutes := versionOneRoutes.Group("/migrations")
	migrationRoutes.PUT("/:id", s.createMigration)
	migrationRoutes.GET("/:id", s.getMigration)
	migrationRoutes.POST("/:id/abort", s.abortMigration)

	// The target host drives these with an Atlas-signed token, not the static token.
	sourceRoutes := router.Group("/v1/migrations")
	sourceRoutes.PUT("/:id/source", s.prepareMigrationSource, s.requireScopes(token.ScopeReadVirtualMachine, token.ScopeMigration))
	sourceRoutes.POST("/:id/snapshot", s.createMigrationSnapshot, s.requireScopes(token.ScopeMigration))
	sourceRoutes.POST("/:id/stream", s.streamMigrationSnapshot, s.requireScopes(token.ScopeMigration))
	sourceRoutes.POST("/:id/stop", s.stopMigrationSource, s.requireScopes(token.ScopeMigration))
	sourceRoutes.DELETE("/:id", s.deleteMigrationSource, s.requireScopes(token.ScopeMigration))
}
