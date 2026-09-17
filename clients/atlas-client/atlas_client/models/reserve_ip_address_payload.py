from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast






T = TypeVar("T", bound="ReserveIPAddressPayload")



@_attrs_define
class ReserveIPAddressPayload:
    """ Optionally reserve an address the tenant already holds.

        Attributes:
            ip_address_id (None | str | Unset):
     """

    ip_address_id: None | str | Unset = UNSET





    def to_dict(self) -> dict[str, Any]:
        ip_address_id: None | str | Unset
        if isinstance(self.ip_address_id, Unset):
            ip_address_id = UNSET
        else:
            ip_address_id = self.ip_address_id


        field_dict: dict[str, Any] = {}

        field_dict.update({
        })
        if ip_address_id is not UNSET:
            field_dict["ip_address_id"] = ip_address_id

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        def _parse_ip_address_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        ip_address_id = _parse_ip_address_id(d.pop("ip_address_id", UNSET))


        reserve_ip_address_payload = cls(
            ip_address_id=ip_address_id,
        )

        return reserve_ip_address_payload

