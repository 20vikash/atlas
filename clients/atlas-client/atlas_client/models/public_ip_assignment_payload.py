from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset







T = TypeVar("T", bound="PublicIPAssignmentPayload")



@_attrs_define
class PublicIPAssignmentPayload:
    """ The public IP to attach.

        Attributes:
            public_ip (str): A reserved direct public IP, or auto for automatic selection.
     """

    public_ip: str





    def to_dict(self) -> dict[str, Any]:
        public_ip = self.public_ip


        field_dict: dict[str, Any] = {}

        field_dict.update({
            "public_ip": public_ip,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        public_ip = d.pop("public_ip")

        public_ip_assignment_payload = cls(
            public_ip=public_ip,
        )

        return public_ip_assignment_payload

