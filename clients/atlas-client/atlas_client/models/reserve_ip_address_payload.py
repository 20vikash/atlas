from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.reserve_ip_address_payload_source import ReserveIPAddressPayloadSource
from ..types import UNSET, Unset






T = TypeVar("T", bound="ReserveIPAddressPayload")



@_attrs_define
class ReserveIPAddressPayload:
    """ Select the source of an IP address reservation.

        Attributes:
            source (ReserveIPAddressPayloadSource | Unset):  Default: ReserveIPAddressPayloadSource.POOL.
     """

    source: ReserveIPAddressPayloadSource | Unset = ReserveIPAddressPayloadSource.POOL





    def to_dict(self) -> dict[str, Any]:
        source: str | Unset = UNSET
        if not isinstance(self.source, Unset):
            source = self.source.value



        field_dict: dict[str, Any] = {}

        field_dict.update({
        })
        if source is not UNSET:
            field_dict["source"] = source

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _source = d.pop("source", UNSET)
        source: ReserveIPAddressPayloadSource | Unset
        if isinstance(_source,  Unset):
            source = UNSET
        else:
            source = ReserveIPAddressPayloadSource(_source)




        reserve_ip_address_payload = cls(
            source=source,
        )

        return reserve_ip_address_payload

