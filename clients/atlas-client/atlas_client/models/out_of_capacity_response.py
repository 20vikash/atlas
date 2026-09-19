from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast

if TYPE_CHECKING:
  from ..models.out_of_capacity_error import OutOfCapacityError





T = TypeVar("T", bound="OutOfCapacityResponse")



@_attrs_define
class OutOfCapacityResponse:
    """ The JSON body of an unavailable capacity response.

        Attributes:
            error (OutOfCapacityError): The error returned when no host can accept the VM.
     """

    error: OutOfCapacityError
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.out_of_capacity_error import OutOfCapacityError # noqa: PLC0415
        error = self.error.to_dict()


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "error": error,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.out_of_capacity_error import OutOfCapacityError # noqa: PLC0415
        d = dict(src_dict)
        error = OutOfCapacityError.from_dict(d.pop("error"))




        out_of_capacity_response = cls(
            error=error,
        )


        out_of_capacity_response.additional_properties = d
        return out_of_capacity_response

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
