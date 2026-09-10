from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.ip_address_response import IPAddressResponse
from ...types import UNSET, Unset
from typing import cast



def _get_kwargs(
    ip_address_id: str,
    *,
    x_tenant_id: int | Unset = UNSET,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(x_tenant_id, Unset):
        headers["X-Tenant-ID"] = str(x_tenant_id)




    

    

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/api/atlas/ip-addresses/{ip_address_id}".format(ip_address_id=quote(str(ip_address_id), safe=""),),
    }


    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> IPAddressResponse | None:
    if response.status_code == 200:
        response_200 = IPAddressResponse.from_dict(response.json())



        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[IPAddressResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    ip_address_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int | Unset = UNSET,

) -> Response[IPAddressResponse]:
    """ Get IP address

     Returns one tenant IP address with its attachment state and VM assignment.

    Args:
        ip_address_id (str):
        x_tenant_id (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IPAddressResponse]
     """


    kwargs = _get_kwargs(
        ip_address_id=ip_address_id,
x_tenant_id=x_tenant_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    ip_address_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int | Unset = UNSET,

) -> IPAddressResponse | None:
    """ Get IP address

     Returns one tenant IP address with its attachment state and VM assignment.

    Args:
        ip_address_id (str):
        x_tenant_id (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IPAddressResponse
     """


    return sync_detailed(
        ip_address_id=ip_address_id,
client=client,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    ip_address_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int | Unset = UNSET,

) -> Response[IPAddressResponse]:
    """ Get IP address

     Returns one tenant IP address with its attachment state and VM assignment.

    Args:
        ip_address_id (str):
        x_tenant_id (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IPAddressResponse]
     """


    kwargs = _get_kwargs(
        ip_address_id=ip_address_id,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    ip_address_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int | Unset = UNSET,

) -> IPAddressResponse | None:
    """ Get IP address

     Returns one tenant IP address with its attachment state and VM assignment.

    Args:
        ip_address_id (str):
        x_tenant_id (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IPAddressResponse
     """


    return (await asyncio_detailed(
        ip_address_id=ip_address_id,
client=client,
x_tenant_id=x_tenant_id,

    )).parsed
