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
            is_termination_protected (bool):
     """

    is_termination_protected: bool





    def to_dict(self) -> dict[str, Any]:
        is_termination_protected = self.is_termination_protected


        field_dict: dict[str, Any] = {}

        field_dict.update({
            "is_termination_protected": is_termination_protected,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        is_termination_protected = d.pop("is_termination_protected")

        termination_protection_payload = cls(
            is_termination_protected=is_termination_protected,
        )

        return termination_protection_payload

