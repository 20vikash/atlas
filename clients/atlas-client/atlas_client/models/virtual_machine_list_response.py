from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast

if TYPE_CHECKING:
  from ..models.virtual_machine_list_response_tags import VirtualMachineListResponseTags





T = TypeVar("T", bound="VirtualMachineListResponse")



@_attrs_define
class VirtualMachineListResponse:
    """ A stored virtual machine with its last known host state.

        Attributes:
            architecture (str):
            cpu_millicores (int):
            created_at (int):
            disk_mib (int):
            id (str):
            image_id (str):
            last_known_state (str):
            memory_mib (int):
            sleep_after_idle_seconds (int):
            state_synced_at (int | None):
            tags (VirtualMachineListResponseTags):
            tenant_id (int):
     """

    architecture: str
    cpu_millicores: int
    created_at: int
    disk_mib: int
    id: str
    image_id: str
    last_known_state: str
    memory_mib: int
    sleep_after_idle_seconds: int
    state_synced_at: int | None
    tags: VirtualMachineListResponseTags
    tenant_id: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.virtual_machine_list_response_tags import VirtualMachineListResponseTags # noqa: PLC0415
        architecture = self.architecture

        cpu_millicores = self.cpu_millicores

        created_at = self.created_at

        disk_mib = self.disk_mib

        id = self.id

        image_id = self.image_id

        last_known_state = self.last_known_state

        memory_mib = self.memory_mib

        sleep_after_idle_seconds = self.sleep_after_idle_seconds

        state_synced_at: int | None
        state_synced_at = self.state_synced_at

        tags = self.tags.to_dict()

        tenant_id = self.tenant_id


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "architecture": architecture,
            "cpu_millicores": cpu_millicores,
            "created_at": created_at,
            "disk_mib": disk_mib,
            "id": id,
            "image_id": image_id,
            "last_known_state": last_known_state,
            "memory_mib": memory_mib,
            "sleep_after_idle_seconds": sleep_after_idle_seconds,
            "state_synced_at": state_synced_at,
            "tags": tags,
            "tenant_id": tenant_id,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.virtual_machine_list_response_tags import VirtualMachineListResponseTags # noqa: PLC0415
        d = dict(src_dict)
        architecture = d.pop("architecture")

        cpu_millicores = d.pop("cpu_millicores")

        created_at = d.pop("created_at")

        disk_mib = d.pop("disk_mib")

        id = d.pop("id")

        image_id = d.pop("image_id")

        last_known_state = d.pop("last_known_state")

        memory_mib = d.pop("memory_mib")

        sleep_after_idle_seconds = d.pop("sleep_after_idle_seconds")

        def _parse_state_synced_at(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        state_synced_at = _parse_state_synced_at(d.pop("state_synced_at"))


        tags = VirtualMachineListResponseTags.from_dict(d.pop("tags"))




        tenant_id = d.pop("tenant_id")

        virtual_machine_list_response = cls(
            architecture=architecture,
            cpu_millicores=cpu_millicores,
            created_at=created_at,
            disk_mib=disk_mib,
            id=id,
            image_id=image_id,
            last_known_state=last_known_state,
            memory_mib=memory_mib,
            sleep_after_idle_seconds=sleep_after_idle_seconds,
            state_synced_at=state_synced_at,
            tags=tags,
            tenant_id=tenant_id,
        )


        virtual_machine_list_response.additional_properties = d
        return virtual_machine_list_response

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
