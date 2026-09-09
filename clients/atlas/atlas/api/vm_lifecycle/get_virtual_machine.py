from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.virtual_machine_detail_response import VirtualMachineDetailResponse
from typing import cast



def _get_kwargs(
    virtual_machine_id: str,
    *,
    x_tenant_id: int,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Tenant-ID"] = str(x_tenant_id)




    

    

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/api/atlas/virtual-machines/{virtual_machine_id}".format(virtual_machine_id=quote(str(virtual_machine_id), safe=""),),
    }


    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> VirtualMachineDetailResponse | None:
    if response.status_code == 200:
        response_200 = VirtualMachineDetailResponse.from_dict(response.json())



        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[VirtualMachineDetailResponse]:
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
    x_tenant_id: int,

) -> Response[VirtualMachineDetailResponse]:
    """ Get VM

     Returns the stored VM record together with its desired state and current state.

    Args:
        virtual_machine_id (str):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[VirtualMachineDetailResponse]
     """


    kwargs = _get_kwargs(
        virtual_machine_id=virtual_machine_id,
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
    x_tenant_id: int,

) -> VirtualMachineDetailResponse | None:
    """ Get VM

     Returns the stored VM record together with its desired state and current state.

    Args:
        virtual_machine_id (str):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        VirtualMachineDetailResponse
     """


    return sync_detailed(
        virtual_machine_id=virtual_machine_id,
client=client,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    virtual_machine_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int,

) -> Response[VirtualMachineDetailResponse]:
    """ Get VM

     Returns the stored VM record together with its desired state and current state.

    Args:
        virtual_machine_id (str):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[VirtualMachineDetailResponse]
     """


    kwargs = _get_kwargs(
        virtual_machine_id=virtual_machine_id,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    virtual_machine_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int,

) -> VirtualMachineDetailResponse | None:
    """ Get VM

     Returns the stored VM record together with its desired state and current state.

    Args:
        virtual_machine_id (str):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        VirtualMachineDetailResponse
     """


    return (await asyncio_detailed(
        virtual_machine_id=virtual_machine_id,
client=client,
x_tenant_id=x_tenant_id,

    )).parsed
