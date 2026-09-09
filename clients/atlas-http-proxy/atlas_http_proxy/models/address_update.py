from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AddressUpdate")


@_attrs_define
class AddressUpdate:
    """One address for one site or custom domain.

    Attributes:
        address (str): The backend IPv6 address. Use `-` only for a site to stop its traffic.
    """

    address: str

    def to_dict(self) -> dict[str, Any]:
        address = self.address

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "address": address,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        address = d.pop("address")

        address_update = cls(
            address=address,
        )

        return address_update
