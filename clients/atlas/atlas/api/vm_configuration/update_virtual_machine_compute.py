from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.compute_update_payload import ComputeUpdatePayload
from ...models.virtual_machine_response import VirtualMachineResponse
from ...types import Response


def _get_kwargs(
    virtual_machine_id: str,
    *,
    body: ComputeUpdatePayload,
    x_tenant_id: int,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Tenant-ID"] = str(x_tenant_id)

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/api/atlas/virtual-machines/{virtual_machine_id}/compute".format(
            virtual_machine_id=quote(str(virtual_machine_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> VirtualMachineResponse | None:
    if response.status_code == 202:
        response_202 = VirtualMachineResponse.from_dict(response.json())

        return response_202

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[VirtualMachineResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    virtual_machine_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ComputeUpdatePayload,
    x_tenant_id: int,
) -> Response[VirtualMachineResponse]:
    """Update compute

     Changes the vCPU count, the memory size, and the sleep policy. A vCPU or memory change needs a
    stopped VM.

    With `is_sleepy`, the host sleeps the VM after `idle_timeout_seconds` of no network activity and
    wakes it on the next packet. 0 disables sleep.

    Args:
        virtual_machine_id (str):
        x_tenant_id (int):
        body (ComputeUpdatePayload): New CPU shape, memory shape, and sleep policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[VirtualMachineResponse]
    """

    kwargs = _get_kwargs(
        virtual_machine_id=virtual_machine_id,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    virtual_machine_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ComputeUpdatePayload,
    x_tenant_id: int,
) -> VirtualMachineResponse | None:
    """Update compute

     Changes the vCPU count, the memory size, and the sleep policy. A vCPU or memory change needs a
    stopped VM.

    With `is_sleepy`, the host sleeps the VM after `idle_timeout_seconds` of no network activity and
    wakes it on the next packet. 0 disables sleep.

    Args:
        virtual_machine_id (str):
        x_tenant_id (int):
        body (ComputeUpdatePayload): New CPU shape, memory shape, and sleep policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        VirtualMachineResponse
    """

    return sync_detailed(
        virtual_machine_id=virtual_machine_id,
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    virtual_machine_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ComputeUpdatePayload,
    x_tenant_id: int,
) -> Response[VirtualMachineResponse]:
    """Update compute

     Changes the vCPU count, the memory size, and the sleep policy. A vCPU or memory change needs a
    stopped VM.

    With `is_sleepy`, the host sleeps the VM after `idle_timeout_seconds` of no network activity and
    wakes it on the next packet. 0 disables sleep.

    Args:
        virtual_machine_id (str):
        x_tenant_id (int):
        body (ComputeUpdatePayload): New CPU shape, memory shape, and sleep policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[VirtualMachineResponse]
    """

    kwargs = _get_kwargs(
        virtual_machine_id=virtual_machine_id,
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    virtual_machine_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ComputeUpdatePayload,
    x_tenant_id: int,
) -> VirtualMachineResponse | None:
    """Update compute

     Changes the vCPU count, the memory size, and the sleep policy. A vCPU or memory change needs a
    stopped VM.

    With `is_sleepy`, the host sleeps the VM after `idle_timeout_seconds` of no network activity and
    wakes it on the next packet. 0 disables sleep.

    Args:
        virtual_machine_id (str):
        x_tenant_id (int):
        body (ComputeUpdatePayload): New CPU shape, memory shape, and sleep policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        VirtualMachineResponse
    """

    return (
        await asyncio_detailed(
            virtual_machine_id=virtual_machine_id,
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
