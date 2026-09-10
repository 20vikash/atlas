from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.ip_address_response import IPAddressResponse
from ...models.reserve_ip_address_payload import ReserveIPAddressPayload
from ...types import UNSET, Unset
from typing import cast



def _get_kwargs(
    *,
    body: ReserveIPAddressPayload,
    x_tenant_id: int | Unset = UNSET,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(x_tenant_id, Unset):
        headers["X-Tenant-ID"] = str(x_tenant_id)




    

    

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/api/atlas/ip-addresses",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | IPAddressResponse | None:
    if response.status_code == 201:
        response_201 = IPAddressResponse.from_dict(response.json())



        return response_201

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | IPAddressResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ReserveIPAddressPayload,
    x_tenant_id: int | Unset = UNSET,

) -> Response[Any | IPAddressResponse]:
    """ Reserve IP address

     Reserves an IP address for the tenant. The pool source claims an unowned Atlas address, and the
    provider source creates a provider reservation.

    Args:
        x_tenant_id (int | Unset):
        body (ReserveIPAddressPayload): Select the source of an IP address reservation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | IPAddressResponse]
     """


    kwargs = _get_kwargs(
        body=body,
x_tenant_id=x_tenant_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    *,
    client: AuthenticatedClient | Client,
    body: ReserveIPAddressPayload,
    x_tenant_id: int | Unset = UNSET,

) -> Any | IPAddressResponse | None:
    """ Reserve IP address

     Reserves an IP address for the tenant. The pool source claims an unowned Atlas address, and the
    provider source creates a provider reservation.

    Args:
        x_tenant_id (int | Unset):
        body (ReserveIPAddressPayload): Select the source of an IP address reservation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | IPAddressResponse
     """


    return sync_detailed(
        client=client,
body=body,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ReserveIPAddressPayload,
    x_tenant_id: int | Unset = UNSET,

) -> Response[Any | IPAddressResponse]:
    """ Reserve IP address

     Reserves an IP address for the tenant. The pool source claims an unowned Atlas address, and the
    provider source creates a provider reservation.

    Args:
        x_tenant_id (int | Unset):
        body (ReserveIPAddressPayload): Select the source of an IP address reservation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | IPAddressResponse]
     """


    kwargs = _get_kwargs(
        body=body,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ReserveIPAddressPayload,
    x_tenant_id: int | Unset = UNSET,

) -> Any | IPAddressResponse | None:
    """ Reserve IP address

     Reserves an IP address for the tenant. The pool source claims an unowned Atlas address, and the
    provider source creates a provider reservation.

    Args:
        x_tenant_id (int | Unset):
        body (ReserveIPAddressPayload): Select the source of an IP address reservation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | IPAddressResponse
     """


    return (await asyncio_detailed(
        client=client,
body=body,
x_tenant_id=x_tenant_id,

    )).parsed
