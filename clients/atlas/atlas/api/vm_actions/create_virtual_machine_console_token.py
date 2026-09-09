from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.console_token_payload import ConsoleTokenPayload
from ...models.console_token_response import ConsoleTokenResponse
from ...types import Response


def _get_kwargs(
    virtual_machine_id: str,
    *,
    body: ConsoleTokenPayload,
    x_tenant_id: int,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Tenant-ID"] = str(x_tenant_id)

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/api/atlas/virtual-machines/{virtual_machine_id}/actions/console-token".format(
            virtual_machine_id=quote(str(virtual_machine_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ConsoleTokenResponse | None:
    if response.status_code == 200:
        response_200 = ConsoleTokenResponse.from_dict(response.json())

        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ConsoleTokenResponse]:
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
    body: ConsoleTokenPayload,
    x_tenant_id: int,
) -> Response[ConsoleTokenResponse]:
    """Create console token

     Returns a single-use token for the Atlas realtime TTY or SSH console. The token expires after 60
    seconds.

    Args:
        virtual_machine_id (str):
        x_tenant_id (int):
        body (ConsoleTokenPayload): The console mode that the token opens.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConsoleTokenResponse]
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
    body: ConsoleTokenPayload,
    x_tenant_id: int,
) -> ConsoleTokenResponse | None:
    """Create console token

     Returns a single-use token for the Atlas realtime TTY or SSH console. The token expires after 60
    seconds.

    Args:
        virtual_machine_id (str):
        x_tenant_id (int):
        body (ConsoleTokenPayload): The console mode that the token opens.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConsoleTokenResponse
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
    body: ConsoleTokenPayload,
    x_tenant_id: int,
) -> Response[ConsoleTokenResponse]:
    """Create console token

     Returns a single-use token for the Atlas realtime TTY or SSH console. The token expires after 60
    seconds.

    Args:
        virtual_machine_id (str):
        x_tenant_id (int):
        body (ConsoleTokenPayload): The console mode that the token opens.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConsoleTokenResponse]
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
    body: ConsoleTokenPayload,
    x_tenant_id: int,
) -> ConsoleTokenResponse | None:
    """Create console token

     Returns a single-use token for the Atlas realtime TTY or SSH console. The token expires after 60
    seconds.

    Args:
        virtual_machine_id (str):
        x_tenant_id (int):
        body (ConsoleTokenPayload): The console mode that the token opens.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConsoleTokenResponse
    """

    return (
        await asyncio_detailed(
            virtual_machine_id=virtual_machine_id,
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
