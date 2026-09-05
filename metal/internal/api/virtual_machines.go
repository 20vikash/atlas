package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/frappe/atlas/metal/internal/vm"
)

// @Summary	Create or confirm a virtual machine reservation
// @Tags		vms
// @Accept		json
// @Produce	json
// @Param		id		path		string			true	"Virtual machine identifier"
// @Param		body	body		createRequest	true	"Virtual machine specification"
// @Success	202		{object}	virtualMachineResponse
// @Failure	400		{object}	errorResponse
// @Failure	409		{object}	errorResponse
// @Router		/vms/{id} [put]
func (s *Server) createVirtualMachine(c echo.Context) error {
	virtualMachineID := c.Param("id")
	if !validResourceID(virtualMachineID) {
		return badRequest("invalid virtual machine identifier")
	}

	var request createRequest
	if err := c.Bind(&request); err != nil {
		return badRequest("invalid JSON request")
	}
	if err := request.validate(); err != nil {
		return badRequest(err.Error())
	}

	specification := request.spec()
	information, err := s.virtualMachineManager.Create(c.Request().Context(), virtualMachineID, specification)
	if err != nil {
		return err
	}

	s.wakeReconciler()
	return c.JSON(http.StatusAccepted, toVirtualMachine(information))
}

// @Summary	List virtual machines
// @Tags		vms
// @Produce	json
// @Success	200	{object}	virtualMachineListResponse
// @Router		/vms [get]
func (s *Server) listVirtualMachines(c echo.Context) error {
	ctx := c.Request().Context()
	virtualMachines, err := s.virtualMachineManager.List(ctx)
	if err != nil {
		return err
	}

	responses := make([]virtualMachineResponse, 0, len(virtualMachines))
	for _, information := range virtualMachines {
		responses = append(responses, toVirtualMachine(information))
	}

	return c.JSON(http.StatusOK, virtualMachineListResponse{VMs: responses})
}

// @Summary	Get a virtual machine
// @Tags		vms
// @Produce	json
// @Param		id	path		string	true	"Virtual machine identifier"
// @Success	200	{object}	virtualMachineResponse
// @Failure	404	{object}	errorResponse
// @Router		/vms/{id} [get]
func (s *Server) getVirtualMachine(c echo.Context) error {
	information, err := s.loadVirtualMachine(c)
	if err != nil {
		return err
	}
	return s.respondWithVirtualMachine(c, http.StatusOK, information)
}

func (s *Server) loadVirtualMachine(c echo.Context) (vm.Info, error) {
	return s.virtualMachineManager.Information(c.Request().Context(), c.Param("id"))
}

func (s *Server) respondWithVirtualMachine(c echo.Context, status int, information vm.Info) error {
	return c.JSON(status, toVirtualMachine(information))
}
