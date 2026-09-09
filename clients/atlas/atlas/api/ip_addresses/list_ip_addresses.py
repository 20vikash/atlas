from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.page_ip_address_response import PageIPAddressResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
    x_tenant_id: int,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Tenant-ID"] = str(x_tenant_id)

    params: dict[str, Any] = {}

    params["offset"] = offset

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/api/atlas/ip-addresses",
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> PageIPAddressResponse | None:
    if response.status_code == 200:
        response_200 = PageIPAddressResponse.from_dict(response.json())

        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PageIPAddressResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
    x_tenant_id: int,
) -> Response[PageIPAddressResponse]:
    """List IP addresses

     Returns one page of IP addresses reserved by the tenant in newest-first order.

    Args:
        offset (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PageIPAddressResponse]
    """

    kwargs = _get_kwargs(
        offset=offset,
        limit=limit,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
    x_tenant_id: int,
) -> PageIPAddressResponse | None:
    """List IP addresses

     Returns one page of IP addresses reserved by the tenant in newest-first order.

    Args:
        offset (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PageIPAddressResponse
    """

    return sync_detailed(
        client=client,
        offset=offset,
        limit=limit,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
    x_tenant_id: int,
) -> Response[PageIPAddressResponse]:
    """List IP addresses

     Returns one page of IP addresses reserved by the tenant in newest-first order.

    Args:
        offset (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PageIPAddressResponse]
    """

    kwargs = _get_kwargs(
        offset=offset,
        limit=limit,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    offset: int | Unset = 0,
    limit: int | Unset = 20,
    x_tenant_id: int,
) -> PageIPAddressResponse | None:
    """List IP addresses

     Returns one page of IP addresses reserved by the tenant in newest-first order.

    Args:
        offset (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PageIPAddressResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            offset=offset,
            limit=limit,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
