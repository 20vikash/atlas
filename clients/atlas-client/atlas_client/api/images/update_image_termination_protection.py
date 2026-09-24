from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.api_error_response import ApiErrorResponse
from ...models.image_response import ImageResponse
from ...models.termination_protection_payload import TerminationProtectionPayload
from typing import cast



def _get_kwargs(
    image_id: str,
    *,
    body: TerminationProtectionPayload,
    x_tenant_id: int,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Tenant-ID"] = str(x_tenant_id)




    

    

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/api/atlas/images/{image_id}/termination-protection".format(image_id=quote(str(image_id), safe=""),),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ApiErrorResponse | ImageResponse | None:
    if response.status_code == 202:
        response_202 = ImageResponse.from_dict(response.json())



        return response_202

    if response.status_code == 400:
        response_400 = ApiErrorResponse.from_dict(response.json())



        return response_400

    if response.status_code == 401:
        response_401 = ApiErrorResponse.from_dict(response.json())



        return response_401

    if response.status_code == 403:
        response_403 = ApiErrorResponse.from_dict(response.json())



        return response_403

    if response.status_code == 404:
        response_404 = ApiErrorResponse.from_dict(response.json())



        return response_404

    if response.status_code == 500:
        response_500 = ApiErrorResponse.from_dict(response.json())



        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[ApiErrorResponse | ImageResponse]:
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
    body: TerminationProtectionPayload,
    x_tenant_id: int,

) -> Response[ApiErrorResponse | ImageResponse]:
    """ Update termination protection

     Sets or clears termination protection. Deletion of a protected image is refused until a later
    request clears it. A `system` image is protected when it is created.

    Args:
        image_id (str):
        x_tenant_id (int):
        body (TerminationProtectionPayload): The termination protection state to store.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiErrorResponse | ImageResponse]
     """


    kwargs = _get_kwargs(
        image_id=image_id,
body=body,
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
    body: TerminationProtectionPayload,
    x_tenant_id: int,

) -> ApiErrorResponse | ImageResponse | None:
    """ Update termination protection

     Sets or clears termination protection. Deletion of a protected image is refused until a later
    request clears it. A `system` image is protected when it is created.

    Args:
        image_id (str):
        x_tenant_id (int):
        body (TerminationProtectionPayload): The termination protection state to store.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiErrorResponse | ImageResponse
     """


    return sync_detailed(
        image_id=image_id,
client=client,
body=body,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    image_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: TerminationProtectionPayload,
    x_tenant_id: int,

) -> Response[ApiErrorResponse | ImageResponse]:
    """ Update termination protection

     Sets or clears termination protection. Deletion of a protected image is refused until a later
    request clears it. A `system` image is protected when it is created.

    Args:
        image_id (str):
        x_tenant_id (int):
        body (TerminationProtectionPayload): The termination protection state to store.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiErrorResponse | ImageResponse]
     """


    kwargs = _get_kwargs(
        image_id=image_id,
body=body,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    image_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: TerminationProtectionPayload,
    x_tenant_id: int,

) -> ApiErrorResponse | ImageResponse | None:
    """ Update termination protection

     Sets or clears termination protection. Deletion of a protected image is refused until a later
    request clears it. A `system` image is protected when it is created.

    Args:
        image_id (str):
        x_tenant_id (int):
        body (TerminationProtectionPayload): The termination protection state to store.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiErrorResponse | ImageResponse
     """


    return (await asyncio_detailed(
        image_id=image_id,
client=client,
body=body,
x_tenant_id=x_tenant_id,

    )).parsed
