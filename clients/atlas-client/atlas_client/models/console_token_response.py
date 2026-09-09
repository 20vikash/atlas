from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.console_token_response_mode import ConsoleTokenResponseMode






T = TypeVar("T", bound="ConsoleTokenResponse")



@_attrs_define
class ConsoleTokenResponse:
    """ A single-use console token.

        Attributes:
            expires_in (int):
            mode (ConsoleTokenResponseMode):
            token (str):
     """

    expires_in: int
    mode: ConsoleTokenResponseMode
    token: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        expires_in = self.expires_in

        mode = self.mode.value

        token = self.token


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "expires_in": expires_in,
            "mode": mode,
            "token": token,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expires_in = d.pop("expires_in")

        mode = ConsoleTokenResponseMode(d.pop("mode"))




        token = d.pop("token")

        console_token_response = cls(
            expires_in=expires_in,
            mode=mode,
            token=token,
        )


        console_token_response.additional_properties = d
        return console_token_response

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
