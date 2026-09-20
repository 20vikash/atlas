from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset







T = TypeVar("T", bound="TerminationProtectionPayload")



@_attrs_define
class TerminationProtectionPayload:
    """ The termination protection state to store.

        Attributes:
            enabled (bool):
     """

    enabled: bool





    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled


        field_dict: dict[str, Any] = {}

        field_dict.update({
            "enabled": enabled,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        termination_protection_payload = cls(
            enabled=enabled,
        )

        return termination_protection_payload

