package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/frappe/atlas/metal/internal/host"
	"github.com/frappe/atlas/metal/internal/network"
	"github.com/frappe/atlas/metal/internal/vm"
)

// syncRequest carries the complete controller-owned host state. Every set is
// complete: a missing field is rejected rather than read as an empty set.
type syncRequest struct {
	WireGuardPeers        []wireGuardPeerRequest `json:"wireguard_peers"`
	Images                []imageRequest         `json:"images"`
	PrivilegedVMAddresses []string               `json:"privileged_vm_addresses"`
}

// wireGuardPeerRequest is one desired WireGuard peer.
type wireGuardPeerRequest struct {
	Node      string `json:"node"`
	NodeID    uint32 `json:"node_id"`
	PublicKey string `json:"public_key"`
	Address   string `json:"address"`
}

// syncResponse returns host capacity in the same exchange as the sync.
type syncResponse struct {
	Capacity capacityResponse `json:"capacity"`
}

// capacityResponse is what the controller needs to place the next VM.
type capacityResponse struct {
	TotalCPUCount       int `json:"total_cpu_count"`
	AvailableCPUCount   int `json:"available_cpu_count"`
	VirtualMachineCount int `json:"virtual_machine_count"`
	TotalMemoryMiB      int `json:"total_memory_mib"`
	AvailableMemoryMiB  int `json:"available_memory_mib"`
	TotalStorageMiB     int `json:"total_storage_mib"`
	AvailableStorageMiB int `json:"available_storage_mib"`
}

// @Summary	Exchange controller and host state
// @Description	Replace the controller-owned host sets and return current host capacity.
// @ID			synchronizeHost
// @Tags		Host synchronization
// @Accept		json
// @Produce	json
// @Security	BearerAuth
// @Param		request	body		syncRequest	true	"Complete controller state"
// @Success	200		{object}	syncResponse
// @Failure	400		{object}	errorResponse
// @Failure	401		{object}	errorResponse
// @Failure	409		{object}	errorResponse
// @Failure	422		{object}	errorResponse
// @Failure	500		{object}	errorResponse
// @Router		/v1/sync [post]
func (s *Server) exchangeControllerState(c echo.Context) error {
	var request syncRequest
	if err := decodeJSONRequest(c, &request); err != nil {
		return err
	}
	if request.WireGuardPeers == nil {
		return badRequest("wireguard_peers is required")
	}
	if request.Images == nil {
		return badRequest("images is required")
	}
	// A missing field would read as an empty set and clear the whitelist.
	if request.PrivilegedVMAddresses == nil {
		return badRequest("privileged_vm_addresses is required")
	}
	for _, image := range request.Images {
		if err := image.validate(); err != nil {
			return badRequest(err.Error())
		}
	}

	capacity, err := s.hostService.Synchronize(c.Request().Context(), host.DesiredState{
		WireGuardPeers: request.wireGuardPeers(), Images: request.imagePolicies(),
		PrivilegedVirtualMachineAddresses: request.PrivilegedVMAddresses,
	})
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, syncResponse{Capacity: capacityResponseFromHost(capacity)})
}

// imagePolicies converts the requested images into image policies.
func (request syncRequest) imagePolicies() []vm.Image {
	images := make([]vm.Image, 0, len(request.Images))
	for _, image := range request.Images {
		images = append(images, image.specification())
	}
	return images
}

// wireGuardPeers converts the requested peers into the network form.
func (request syncRequest) wireGuardPeers() []network.WireGuardPeer {
	peers := make([]network.WireGuardPeer, 0, len(request.WireGuardPeers))
	for _, peer := range request.WireGuardPeers {
		peers = append(peers, network.WireGuardPeer{
			Node:      peer.Node,
			NodeID:    peer.NodeID,
			PublicKey: peer.PublicKey,
			Address:   peer.Address,
		})
	}
	return peers
}

// capacityResponseFromHost converts host capacity into the response form.
func capacityResponseFromHost(capacity host.Capacity) capacityResponse {
	return capacityResponse{
		TotalCPUCount:       capacity.TotalCPUCount,
		AvailableCPUCount:   capacity.AvailableCPUCount,
		VirtualMachineCount: capacity.VirtualMachineCount,
		TotalMemoryMiB:      capacity.TotalMemoryMiB,
		AvailableMemoryMiB:  capacity.AvailableMemoryMiB,
		TotalStorageMiB:     capacity.TotalStorageMiB,
		AvailableStorageMiB: capacity.AvailableStorageMiB,
	}
}
