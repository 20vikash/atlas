package api

import "github.com/labstack/echo/v4"

// registerRoutes binds every handler. Routes that change desired state use PUT
// and are idempotent. POST is used only where a request is an action or creates
// an addressable resource.
func (s *Server) registerRoutes(router *echo.Echo) {
	router.GET("/health", s.checkHealth)
	router.GET("/docs", s.showDocumentation)
	router.GET("/docs/swagger.json", s.getOpenAPISpecification)

	versionOneRoutes := router.Group("/v1")
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
}
