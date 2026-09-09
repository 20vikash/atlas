from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.console_token_payload_mode import ConsoleTokenPayloadMode
from ..types import UNSET, Unset






T = TypeVar("T", bound="ConsoleTokenPayload")



@_attrs_define
class ConsoleTokenPayload:
    """ The console mode that the token opens.

        Attributes:
            mode (ConsoleTokenPayloadMode | Unset):  Default: ConsoleTokenPayloadMode.TTY.
     """

    mode: ConsoleTokenPayloadMode | Unset = ConsoleTokenPayloadMode.TTY





    def to_dict(self) -> dict[str, Any]:
        mode: str | Unset = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode.value



        field_dict: dict[str, Any] = {}

        field_dict.update({
        })
        if mode is not UNSET:
            field_dict["mode"] = mode

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _mode = d.pop("mode", UNSET)
        mode: ConsoleTokenPayloadMode | Unset
        if isinstance(_mode,  Unset):
            mode = UNSET
        else:
            mode = ConsoleTokenPayloadMode(_mode)




        console_token_payload = cls(
            mode=mode,
        )

        return console_token_payload

