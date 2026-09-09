from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast






T = TypeVar("T", bound="VirtualMachineDetailResponse")



@_attrs_define
class VirtualMachineDetailResponse:
    """ A stored virtual machine with its live host state.

        Attributes:
            created_at (int):
            current_state (str):
            desired_state (None | str):
            disk_mib (int):
            id (str):
            image_id (str):
            memory_mib (int):
            sleep_after_idle_seconds (int):
            tenant_id (int):
            vcpus (int):
     """

    created_at: int
    current_state: str
    desired_state: None | str
    disk_mib: int
    id: str
    image_id: str
    memory_mib: int
    sleep_after_idle_seconds: int
    tenant_id: int
    vcpus: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        created_at = self.created_at

        current_state = self.current_state

        desired_state: None | str
        desired_state = self.desired_state

        disk_mib = self.disk_mib

        id = self.id

        image_id = self.image_id

        memory_mib = self.memory_mib

        sleep_after_idle_seconds = self.sleep_after_idle_seconds

        tenant_id = self.tenant_id

        vcpus = self.vcpus


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "created_at": created_at,
            "current_state": current_state,
            "desired_state": desired_state,
            "disk_mib": disk_mib,
            "id": id,
            "image_id": image_id,
            "memory_mib": memory_mib,
            "sleep_after_idle_seconds": sleep_after_idle_seconds,
            "tenant_id": tenant_id,
            "vcpus": vcpus,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        created_at = d.pop("created_at")

        current_state = d.pop("current_state")

        def _parse_desired_state(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        desired_state = _parse_desired_state(d.pop("desired_state"))


        disk_mib = d.pop("disk_mib")

        id = d.pop("id")

        image_id = d.pop("image_id")

        memory_mib = d.pop("memory_mib")

        sleep_after_idle_seconds = d.pop("sleep_after_idle_seconds")

        tenant_id = d.pop("tenant_id")

        vcpus = d.pop("vcpus")

        virtual_machine_detail_response = cls(
            created_at=created_at,
            current_state=current_state,
            desired_state=desired_state,
            disk_mib=disk_mib,
            id=id,
            image_id=image_id,
            memory_mib=memory_mib,
            sleep_after_idle_seconds=sleep_after_idle_seconds,
            tenant_id=tenant_id,
            vcpus=vcpus,
        )


        virtual_machine_detail_response.additional_properties = d
        return virtual_machine_detail_response

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
