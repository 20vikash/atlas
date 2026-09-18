from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset






T = TypeVar("T", bound="ConfigureWebhooksPayload")



@_attrs_define
class ConfigureWebhooksPayload:
    """ The destination of the event deliveries of one Central.

        Attributes:
            request_url (str): HTTP or HTTPS URL that receives every delivery.
            webhook_secret (str): Shared secret that signs every delivery.
            central_id (int | Unset): Receiving Central. Default: 1.
            enabled (bool | Unset):  Default: True.
     """

    request_url: str
    webhook_secret: str
    central_id: int | Unset = 1
    enabled: bool | Unset = True





    def to_dict(self) -> dict[str, Any]:
        request_url = self.request_url

        webhook_secret = self.webhook_secret

        central_id = self.central_id

        enabled = self.enabled


        field_dict: dict[str, Any] = {}

        field_dict.update({
            "request_url": request_url,
            "webhook_secret": webhook_secret,
        })
        if central_id is not UNSET:
            field_dict["central_id"] = central_id
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_url = d.pop("request_url")

        webhook_secret = d.pop("webhook_secret")

        central_id = d.pop("central_id", UNSET)

        enabled = d.pop("enabled", UNSET)

        configure_webhooks_payload = cls(
            request_url=request_url,
            webhook_secret=webhook_secret,
            central_id=central_id,
            enabled=enabled,
        )

        return configure_webhooks_payload

