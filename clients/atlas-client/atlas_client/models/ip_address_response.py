from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast






T = TypeVar("T", bound="IPAddressResponse")



@_attrs_define
class IPAddressResponse:
    """ A tenant public IPv4 address.

        Attributes:
            address (str):
            created_at (int):
            id (str):
            state (str):
            tenant_id (int):
            virtual_machine_id (None | str):
     """

    address: str
    created_at: int
    id: str
    state: str
    tenant_id: int
    virtual_machine_id: None | str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        address = self.address

        created_at = self.created_at

        id = self.id

        state = self.state

        tenant_id = self.tenant_id

        virtual_machine_id: None | str
        virtual_machine_id = self.virtual_machine_id


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "address": address,
            "created_at": created_at,
            "id": id,
            "state": state,
            "tenant_id": tenant_id,
            "virtual_machine_id": virtual_machine_id,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        address = d.pop("address")

        created_at = d.pop("created_at")

        id = d.pop("id")

        state = d.pop("state")

        tenant_id = d.pop("tenant_id")

        def _parse_virtual_machine_id(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        virtual_machine_id = _parse_virtual_machine_id(d.pop("virtual_machine_id"))


        ip_address_response = cls(
            address=address,
            created_at=created_at,
            id=id,
            state=state,
            tenant_id=tenant_id,
            virtual_machine_id=virtual_machine_id,
        )


        ip_address_response.additional_properties = d
        return ip_address_response

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
