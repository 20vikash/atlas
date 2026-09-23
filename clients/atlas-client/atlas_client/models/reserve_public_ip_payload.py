from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.reserve_public_ip_payload_version import ReservePublicIPPayloadVersion






T = TypeVar("T", bound="ReservePublicIPPayload")



@_attrs_define
class ReservePublicIPPayload:
    """ Select the IP version of one direct reservation.

        Attributes:
            version (ReservePublicIPPayloadVersion):
     """

    version: ReservePublicIPPayloadVersion





    def to_dict(self) -> dict[str, Any]:
        version = self.version.value


        field_dict: dict[str, Any] = {}

        field_dict.update({
            "version": version,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = ReservePublicIPPayloadVersion(d.pop("version"))




        reserve_public_ip_payload = cls(
            version=version,
        )

        return reserve_public_ip_payload

