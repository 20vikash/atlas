package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/frappe/atlas/metal/internal/host"
	"github.com/frappe/atlas/metal/internal/network"
	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/token"
	"github.com/frappe/atlas/metal/internal/vm"
)

// syncRequest carries the complete controller-owned host state. Every set is
// complete: a missing field is rejected rather than read as an empty set.
type syncRequest struct {
	WireGuardPeers        []wireGuardPeerRequest `json:"wireguard_peers"`
	Images                []imageRequest         `json:"images"`
	PrivilegedVMAddresses []string               `json:"privileged_vm_addresses"`
	// JWT is optional. A missing object keeps the keys the host already trusts.
	JWT *jwtRequest `json:"jwt,omitempty"`
}

// jwtRequest carries the Atlas keys that sign the tokens this host accepts.
type jwtRequest struct {
	Issuer     string                `json:"issuer"`
	Receiver   string                `json:"receiver"`
	PublicKeys []jwtPublicKeyRequest `json:"public_keys"`
}

// jwtPublicKeyRequest is one Atlas signing key in base64url without padding.
type jwtPublicKeyRequest struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

// wireGuardPeerRequest is one desired WireGuard peer.
type wireGuardPeerRequest struct {
	Node      string `json:"node"`
	NodeID    uint32 `json:"node_id"`
	PublicKey string `json:"public_key"`
	Address   string `json:"address"`
}

// syncResponse returns host capacity and virtual machine state in the same
// exchange as the sync.
type syncResponse struct {
	Capacity capacityResponse `json:"capacity"`
	// VirtualMachines maps a VM identifier to its last observed state.
	VirtualMachines map[string]virtualMachineStateResponse `json:"virtual_machines"`
}

// virtualMachineStateResponse is the state of one virtual machine on this host.
type virtualMachineStateResponse struct {
	Status string `json:"status"`
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
// @Failure	503		{object}	errorResponse
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

	if request.JWT != nil {
		if err := s.trustedKeys.Replace(request.JWT.trustedKeys()); err != nil {
			return err
		}
	}

	result, err := s.hostService.Synchronize(c.Request().Context(), host.DesiredState{
		WireGuardPeers: request.wireGuardPeers(), Images: request.imagePolicies(),
		PrivilegedVirtualMachineAddresses: request.PrivilegedVMAddresses,
	})
	if err != nil {
		return synchronizationFailure(err)
	}

	return c.JSON(http.StatusOK, syncResponse{
		Capacity:        capacityResponseFromHost(result.Capacity),
		VirtualMachines: virtualMachineStateResponses(result.VirtualMachineStates),
	})
}

// synchronizationFailure keeps a host not-found out of the response. This
// endpoint addresses no resource, so a 404 stops the controller when it must
// try the request again.
func synchronizationFailure(err error) error {
	if errors.Is(err, vm.ErrNotFound) || errors.Is(err, storage.ErrNotFound) {
		unavailable := newAPIError(http.StatusServiceUnavailable, "unavailable", "host state is incomplete")
		return fmt.Errorf("%w: %w", unavailable, err)
	}
	return err
}

// trustedKeys converts the requested keys into the trust state form.
func (request jwtRequest) trustedKeys() token.TrustedKeys {
	publicKeys := make([]token.PublicKey, 0, len(request.PublicKeys))
	for _, publicKey := range request.PublicKeys {
		publicKeys = append(publicKeys, token.PublicKey{ID: publicKey.ID, Key: publicKey.Key})
	}

	return token.TrustedKeys{Issuer: request.Issuer, Receiver: request.Receiver, PublicKeys: publicKeys}
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

// virtualMachineStateResponses converts host states into the response form.
func virtualMachineStateResponses(states map[string]vm.State) map[string]virtualMachineStateResponse {
	responses := make(map[string]virtualMachineStateResponse, len(states))
	for identifier, state := range states {
		responses[identifier] = virtualMachineStateResponse{Status: string(state)}
	}
	return responses
}
