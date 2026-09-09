from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.image_response import ImageResponse
from ...types import Response


def _get_kwargs(
    image_id: str,
    *,
    x_tenant_id: int,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Tenant-ID"] = str(x_tenant_id)

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/api/atlas/images/{image_id}".format(
            image_id=quote(str(image_id), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ImageResponse | None:
    if response.status_code == 200:
        response_200 = ImageResponse.from_dict(response.json())

        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[ImageResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    image_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int,
) -> Response[ImageResponse]:
    """Get image

     Returns one tenant image with its artifact metadata and transfer state.

    Args:
        image_id (str):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ImageResponse]
    """

    kwargs = _get_kwargs(
        image_id=image_id,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    image_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int,
) -> ImageResponse | None:
    """Get image

     Returns one tenant image with its artifact metadata and transfer state.

    Args:
        image_id (str):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ImageResponse
    """

    return sync_detailed(
        image_id=image_id,
        client=client,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    image_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int,
) -> Response[ImageResponse]:
    """Get image

     Returns one tenant image with its artifact metadata and transfer state.

    Args:
        image_id (str):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ImageResponse]
    """

    kwargs = _get_kwargs(
        image_id=image_id,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    image_id: str,
    *,
    client: AuthenticatedClient | Client,
    x_tenant_id: int,
) -> ImageResponse | None:
    """Get image

     Returns one tenant image with its artifact metadata and transfer state.

    Args:
        image_id (str):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ImageResponse
    """

    return (
        await asyncio_detailed(
            image_id=image_id,
            client=client,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
