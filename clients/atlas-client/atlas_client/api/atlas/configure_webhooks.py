from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.configure_webhooks_payload import ConfigureWebhooksPayload
from ...models.webhook_configuration_response import WebhookConfigurationResponse
from typing import cast



def _get_kwargs(
    *,
    body: ConfigureWebhooksPayload,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}


    

    

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/api/atlas/webhooks",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | WebhookConfigurationResponse | None:
    if response.status_code == 200:
        response_200 = WebhookConfigurationResponse.from_dict(response.json())



        return response_200

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | WebhookConfigurationResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ConfigureWebhooksPayload,

) -> Response[Any | WebhookConfigurationResponse]:
    """ Configure webhooks

     Point all Atlas event deliveries for one Central at its receiver. Atlas chooses the events and their
    content. A repeated call refreshes the configuration. Only a Central token, which carries tenant
    `*`, can use this route.

    Args:
        body (ConfigureWebhooksPayload): The destination of the event deliveries of one Central.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | WebhookConfigurationResponse]
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
    client: AuthenticatedClient | Client,
    body: ConfigureWebhooksPayload,

) -> Any | WebhookConfigurationResponse | None:
    """ Configure webhooks

     Point all Atlas event deliveries for one Central at its receiver. Atlas chooses the events and their
    content. A repeated call refreshes the configuration. Only a Central token, which carries tenant
    `*`, can use this route.

    Args:
        body (ConfigureWebhooksPayload): The destination of the event deliveries of one Central.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | WebhookConfigurationResponse
     """


    return sync_detailed(
        client=client,
body=body,

    ).parsed

async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ConfigureWebhooksPayload,

) -> Response[Any | WebhookConfigurationResponse]:
    """ Configure webhooks

     Point all Atlas event deliveries for one Central at its receiver. Atlas chooses the events and their
    content. A repeated call refreshes the configuration. Only a Central token, which carries tenant
    `*`, can use this route.

    Args:
        body (ConfigureWebhooksPayload): The destination of the event deliveries of one Central.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | WebhookConfigurationResponse]
     """


    kwargs = _get_kwargs(
        body=body,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ConfigureWebhooksPayload,

) -> Any | WebhookConfigurationResponse | None:
    """ Configure webhooks

     Point all Atlas event deliveries for one Central at its receiver. Atlas chooses the events and their
    content. A repeated call refreshes the configuration. Only a Central token, which carries tenant
    `*`, can use this route.

    Args:
        body (ConfigureWebhooksPayload): The destination of the event deliveries of one Central.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | WebhookConfigurationResponse
     """


    return (await asyncio_detailed(
        client=client,
body=body,

    )).parsed
