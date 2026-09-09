from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="IPAddressAssignmentPayload")


@_attrs_define
class IPAddressAssignmentPayload:
    """The public IPv4 address to attach.

    Attributes:
        ip_address_id (str):
    """

    ip_address_id: str

    def to_dict(self) -> dict[str, Any]:
        ip_address_id = self.ip_address_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "ip_address_id": ip_address_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ip_address_id = d.pop("ip_address_id")

        ip_address_assignment_payload = cls(
            ip_address_id=ip_address_id,
        )

        return ip_address_assignment_payload
