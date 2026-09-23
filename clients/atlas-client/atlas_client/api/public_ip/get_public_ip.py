from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.public_ip_response import PublicIPResponse
from typing import cast



def _get_kwargs(
    public_ip_id: str,
    *,
    x_tenant_id: int,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Tenant-ID"] = str(x_tenant_id)




    

    

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/api/atlas/public-ips/{public_ip_id}".format(public_ip_id=quote(str(public_ip_id), safe=""),),
    }


    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> PublicIPResponse | None:
    if response.status_code == 200:
        response_200 = PublicIPResponse.from_dict(response.json())



        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[PublicIPResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    public_ip_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int,

) -> Response[PublicIPResponse]:
    """ Get public IP

     Returns one public IP that belongs to the request tenant.

    Args:
        public_ip_id (str):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PublicIPResponse]
     """


    kwargs = _get_kwargs(
        public_ip_id=public_ip_id,
x_tenant_id=x_tenant_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    public_ip_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int,

) -> PublicIPResponse | None:
    """ Get public IP

     Returns one public IP that belongs to the request tenant.

    Args:
        public_ip_id (str):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PublicIPResponse
     """


    return sync_detailed(
        public_ip_id=public_ip_id,
client=client,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    public_ip_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int,

) -> Response[PublicIPResponse]:
    """ Get public IP

     Returns one public IP that belongs to the request tenant.

    Args:
        public_ip_id (str):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PublicIPResponse]
     """


    kwargs = _get_kwargs(
        public_ip_id=public_ip_id,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    public_ip_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int,

) -> PublicIPResponse | None:
    """ Get public IP

     Returns one public IP that belongs to the request tenant.

    Args:
        public_ip_id (str):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PublicIPResponse
     """


    return (await asyncio_detailed(
        public_ip_id=public_ip_id,
client=client,
x_tenant_id=x_tenant_id,

    )).parsed
