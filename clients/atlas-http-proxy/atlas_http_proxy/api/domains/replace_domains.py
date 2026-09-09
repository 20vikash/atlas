from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.http_validation_error import HTTPValidationError
from ...models.map_replaced import MapReplaced
from ...models.replace_domains_values import ReplaceDomainsValues
from ...types import Response


def _get_kwargs(
    *,
    body: ReplaceDomainsValues,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/domains",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> HTTPValidationError | MapReplaced | None:
    if response.status_code == 200:
        response_200 = MapReplaced.from_dict(response.json())

        return response_200

    if response.status_code == 422:
        response_422 = HTTPValidationError.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[HTTPValidationError | MapReplaced]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: ReplaceDomainsValues,
) -> Response[HTTPValidationError | MapReplaced]:
    """Sync domain routes

     Replace every custom-domain route after a controller restart or full reconciliation. Example:
    `www.example.com` routes to `2001:db8::20`.

    Args:
        body (ReplaceDomainsValues): The complete desired custom-domain map.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[HTTPValidationError | MapReplaced]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: ReplaceDomainsValues,
) -> HTTPValidationError | MapReplaced | None:
    """Sync domain routes

     Replace every custom-domain route after a controller restart or full reconciliation. Example:
    `www.example.com` routes to `2001:db8::20`.

    Args:
        body (ReplaceDomainsValues): The complete desired custom-domain map.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        HTTPValidationError | MapReplaced
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: ReplaceDomainsValues,
) -> Response[HTTPValidationError | MapReplaced]:
    """Sync domain routes

     Replace every custom-domain route after a controller restart or full reconciliation. Example:
    `www.example.com` routes to `2001:db8::20`.

    Args:
        body (ReplaceDomainsValues): The complete desired custom-domain map.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[HTTPValidationError | MapReplaced]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: ReplaceDomainsValues,
) -> HTTPValidationError | MapReplaced | None:
    """Sync domain routes

     Replace every custom-domain route after a controller restart or full reconciliation. Example:
    `www.example.com` routes to `2001:db8::20`.

    Args:
        body (ReplaceDomainsValues): The complete desired custom-domain map.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        HTTPValidationError | MapReplaced
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
