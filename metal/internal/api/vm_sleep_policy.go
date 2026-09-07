package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// @Summary	Set virtual machine sleep policy
// @Description	Store the complete sleep policy. The body carries only is_sleepy. The idle timeout is one Metal-wide host value.
// @ID			setVirtualMachineSleepPolicy
// @Tags		Virtual machines
// @Accept		json
// @Produce	json
// @Security	BearerAuth
// @Param		id		path		string				true	"Virtual machine identifier"
// @Param		request	body		sleepPolicyRequest	true	"Complete sleep policy"
// @Success	202		{object}	virtualMachineResponse
// @Failure	400		{object}	errorResponse
// @Failure	401		{object}	errorResponse
// @Failure	404		{object}	errorResponse
// @Failure	409		{object}	errorResponse
// @Failure	500		{object}	errorResponse
// @Router		/v1/vms/{id}/sleep-policy [put]
func (s *Server) setVirtualMachineSleepPolicy(c echo.Context) error {
	identifier, err := virtualMachineID(c)
	if err != nil {
		return err
	}

	var request sleepPolicyRequest
	if err := decodeJSONRequest(c, &request); err != nil {
		return err
	}

	if err := s.virtualMachineManager.SetSleepPolicy(c.Request().Context(), identifier, request.IsSleepy); err != nil {
		return err
	}

	s.wakeReconciler()
	return s.respondWithCurrentVirtualMachine(c, http.StatusAccepted)
}
