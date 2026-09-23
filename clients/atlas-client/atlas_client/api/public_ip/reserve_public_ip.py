from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.public_ip_response import PublicIPResponse
from ...models.reserve_public_ip_payload import ReservePublicIPPayload
from typing import cast



def _get_kwargs(
    *,
    body: ReservePublicIPPayload,
    x_tenant_id: int,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Tenant-ID"] = str(x_tenant_id)




    

    

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/api/atlas/public-ips",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | PublicIPResponse | None:
    if response.status_code == 201:
        response_201 = PublicIPResponse.from_dict(response.json())



        return response_201

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | PublicIPResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ReservePublicIPPayload,
    x_tenant_id: int,

) -> Response[Any | PublicIPResponse]:
    """ Reserve new public IP

     Reserves one direct IPv4 or IPv6 prefix. Atlas selects the pool.

    Args:
        x_tenant_id (int):
        body (ReservePublicIPPayload): Select the IP version of one direct reservation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | PublicIPResponse]
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
    body: ReservePublicIPPayload,
    x_tenant_id: int,

) -> Any | PublicIPResponse | None:
    """ Reserve new public IP

     Reserves one direct IPv4 or IPv6 prefix. Atlas selects the pool.

    Args:
        x_tenant_id (int):
        body (ReservePublicIPPayload): Select the IP version of one direct reservation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | PublicIPResponse
     """


    return sync_detailed(
        client=client,
body=body,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ReservePublicIPPayload,
    x_tenant_id: int,

) -> Response[Any | PublicIPResponse]:
    """ Reserve new public IP

     Reserves one direct IPv4 or IPv6 prefix. Atlas selects the pool.

    Args:
        x_tenant_id (int):
        body (ReservePublicIPPayload): Select the IP version of one direct reservation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | PublicIPResponse]
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
    body: ReservePublicIPPayload,
    x_tenant_id: int,

) -> Any | PublicIPResponse | None:
    """ Reserve new public IP

     Reserves one direct IPv4 or IPv6 prefix. Atlas selects the pool.

    Args:
        x_tenant_id (int):
        body (ReservePublicIPPayload): Select the IP version of one direct reservation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | PublicIPResponse
     """


    return (await asyncio_detailed(
        client=client,
body=body,
x_tenant_id=x_tenant_id,

    )).parsed
