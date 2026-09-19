from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast
from typing import Literal, cast

if TYPE_CHECKING:
  from ..models.api_error_field import ApiErrorField





T = TypeVar("T", bound="OutOfCapacityError")



@_attrs_define
class OutOfCapacityError:
    """ The error returned when no host can accept the VM.

        Attributes:
            code (Literal['out_of_capacity']):
            fields (list[ApiErrorField]):
            message (str):
     """

    code: Literal['out_of_capacity']
    fields: list[ApiErrorField]
    message: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.api_error_field import ApiErrorField # noqa: PLC0415
        code = self.code

        fields = []
        for fields_item_data in self.fields:
            fields_item = fields_item_data.to_dict()
            fields.append(fields_item)



        message = self.message


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "code": code,
            "fields": fields,
            "message": message,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_error_field import ApiErrorField # noqa: PLC0415
        d = dict(src_dict)
        code = cast(Literal['out_of_capacity'] , d.pop("code"))
        if code != 'out_of_capacity':
            raise ValueError(f"code must match const 'out_of_capacity', got '{code}'")

        fields = []
        _fields = d.pop("fields")
        for fields_item_data in (_fields):
            fields_item = ApiErrorField.from_dict(fields_item_data)



            fields.append(fields_item)


        message = d.pop("message")

        out_of_capacity_error = cls(
            code=code,
            fields=fields,
            message=message,
        )


        out_of_capacity_error.additional_properties = d
        return out_of_capacity_error

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
