from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.download_image_artifact import DownloadImageArtifact
from ...models.image_download_response import ImageDownloadResponse
from ...types import UNSET, Response


def _get_kwargs(
    image_id: str,
    *,
    artifact: DownloadImageArtifact,
    x_tenant_id: int,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Tenant-ID"] = str(x_tenant_id)

    params: dict[str, Any] = {}

    json_artifact = artifact.value
    params["artifact"] = json_artifact

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/api/atlas/images/{image_id}/download".format(
            image_id=quote(str(image_id), safe=""),
        ),
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ImageDownloadResponse | None:
    if response.status_code == 200:
        response_200 = ImageDownloadResponse.from_dict(response.json())

        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ImageDownloadResponse]:
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
    artifact: DownloadImageArtifact,
    x_tenant_id: int,
) -> Response[ImageDownloadResponse]:
    """Download image

     Returns a signed download URL for the selected rootfs or kernel artifact. The response includes its
    size, SHA-256 value, and expiry time and cannot be cached.

    Args:
        image_id (str):
        artifact (DownloadImageArtifact):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ImageDownloadResponse]
    """

    kwargs = _get_kwargs(
        image_id=image_id,
        artifact=artifact,
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
    artifact: DownloadImageArtifact,
    x_tenant_id: int,
) -> ImageDownloadResponse | None:
    """Download image

     Returns a signed download URL for the selected rootfs or kernel artifact. The response includes its
    size, SHA-256 value, and expiry time and cannot be cached.

    Args:
        image_id (str):
        artifact (DownloadImageArtifact):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ImageDownloadResponse
    """

    return sync_detailed(
        image_id=image_id,
        client=client,
        artifact=artifact,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    image_id: str,
    *,
    client: AuthenticatedClient | Client,
    artifact: DownloadImageArtifact,
    x_tenant_id: int,
) -> Response[ImageDownloadResponse]:
    """Download image

     Returns a signed download URL for the selected rootfs or kernel artifact. The response includes its
    size, SHA-256 value, and expiry time and cannot be cached.

    Args:
        image_id (str):
        artifact (DownloadImageArtifact):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ImageDownloadResponse]
    """

    kwargs = _get_kwargs(
        image_id=image_id,
        artifact=artifact,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    image_id: str,
    *,
    client: AuthenticatedClient | Client,
    artifact: DownloadImageArtifact,
    x_tenant_id: int,
) -> ImageDownloadResponse | None:
    """Download image

     Returns a signed download URL for the selected rootfs or kernel artifact. The response includes its
    size, SHA-256 value, and expiry time and cannot be cached.

    Args:
        image_id (str):
        artifact (DownloadImageArtifact):
        x_tenant_id (int):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ImageDownloadResponse
    """

    return (
        await asyncio_detailed(
            image_id=image_id,
            client=client,
            artifact=artifact,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
