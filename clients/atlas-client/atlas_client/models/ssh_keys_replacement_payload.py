from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast






T = TypeVar("T", bound="SSHKeysReplacementPayload")



@_attrs_define
class SSHKeysReplacementPayload:
    """ The complete authorized key list.

        Attributes:
            ssh_keys (list[str]):
     """

    ssh_keys: list[str]





    def to_dict(self) -> dict[str, Any]:
        ssh_keys = self.ssh_keys




        field_dict: dict[str, Any] = {}

        field_dict.update({
            "ssh_keys": ssh_keys,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ssh_keys = cast(list[str], d.pop("ssh_keys"))


        ssh_keys_replacement_payload = cls(
            ssh_keys=ssh_keys,
        )

        return ssh_keys_replacement_payload

