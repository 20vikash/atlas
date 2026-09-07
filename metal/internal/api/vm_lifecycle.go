package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/frappe/atlas/metal/internal/vm"
)

// @Summary	Set the virtual machine power state
// @Description	Store the requested running, stopped, or paused state. Reconciliation continues after the response.
// @ID			setVirtualMachinePowerState
// @Tags		Virtual machines
// @Accept		json
// @Produce	json
// @Security	BearerAuth
// @Param		id		path		string		true	"Virtual machine identifier"
// @Param		request	body		powerRequest	true	"Desired power state"
// @Success	202		{object}	virtualMachineResponse
// @Failure	400		{object}	errorResponse
// @Failure	401		{object}	errorResponse
// @Failure	404		{object}	errorResponse
// @Failure	409		{object}	errorResponse
// @Failure	500		{object}	errorResponse
// @Router		/v1/vms/{id}/power [put]
func (s *Server) setVirtualMachinePowerState(c echo.Context) error {
	identifier, err := virtualMachineID(c)
	if err != nil {
		return err
	}
	var request powerRequest
	if err := decodeJSONRequest(c, &request); err != nil {
		return err
	}
	state, err := request.state()
	if err != nil {
		return badRequest(err.Error())
	}
	if request.Warm {
		if state != vm.StateStopped {
			return badRequest("warm is valid only with a stopped state")
		}
		if err := s.virtualMachineManager.StopWarm(c.Request().Context(), identifier); err != nil {
			return err
		}
	} else if err := s.virtualMachineManager.SetPowerState(c.Request().Context(), identifier, state); err != nil {
		return err
	}

	s.wakeReconciler()
	return s.respondWithCurrentVirtualMachine(c, http.StatusAccepted)
}

// @Summary	Restart a virtual machine
// @Description	Store a new restart request. Reconciliation continues after the response.
// @ID			restartVirtualMachine
// @Tags		Virtual machines
// @Produce	json
// @Security	BearerAuth
// @Param		id	path		string	true	"Virtual machine identifier"
// @Success	202	{object}	virtualMachineResponse
// @Failure	400	{object}	errorResponse
// @Failure	401	{object}	errorResponse
// @Failure	404	{object}	errorResponse
// @Failure	409	{object}	errorResponse
// @Failure	500	{object}	errorResponse
// @Router		/v1/vms/{id}/restart [post]
func (s *Server) restartVirtualMachine(c echo.Context) error {
	identifier, err := virtualMachineID(c)
	if err != nil {
		return err
	}
	if err := s.virtualMachineManager.RequestRestart(c.Request().Context(), identifier); err != nil {
		return err
	}

	s.wakeReconciler()
	return s.respondWithCurrentVirtualMachine(c, http.StatusAccepted)
}

// @Summary	Delete a virtual machine
// @Description	Store the destroyed state. Metal removes the virtual machine through reconciliation.
// @ID			deleteVirtualMachine
// @Tags		Virtual machines
// @Produce	json
// @Security	BearerAuth
// @Param		id	path		string	true	"Virtual machine identifier"
// @Success	202	{object}	virtualMachineResponse
// @Failure	400	{object}	errorResponse
// @Failure	401	{object}	errorResponse
// @Failure	404	{object}	errorResponse
// @Failure	500	{object}	errorResponse
// @Router		/v1/vms/{id} [delete]
func (s *Server) deleteVirtualMachine(c echo.Context) error {
	identifier, err := virtualMachineID(c)
	if err != nil {
		return err
	}
	if err := s.virtualMachineManager.Delete(c.Request().Context(), identifier); err != nil {
		return err
	}

	s.wakeReconciler()
	return s.respondWithCurrentVirtualMachine(c, http.StatusAccepted)
}

// respondWithCurrentVirtualMachine rereads the VM and returns it, so a caller
// sees the stored intent its request produced.
func (s *Server) respondWithCurrentVirtualMachine(c echo.Context, status int) error {
	virtualMachine, err := s.loadVirtualMachine(c)
	if err != nil {
		return err
	}
	return s.respondWithVirtualMachine(c, status, virtualMachine)
}
