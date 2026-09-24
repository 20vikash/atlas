from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast






T = TypeVar("T", bound="MemorySnapshotConfigurationPayload")



@_attrs_define
class MemorySnapshotConfigurationPayload:
    """ The virtual machine shape that a warm artifact serves.

        Attributes:
            disk_mib (int | None | Unset): Disk capacity served by the warm artifact in MiB.
            memory_mib (int | None | Unset): Memory capacity served by the warm artifact in MiB.
            virtual_cpu_count (int | None | Unset): Virtual CPU count served by the warm artifact.
     """

    disk_mib: int | None | Unset = UNSET
    memory_mib: int | None | Unset = UNSET
    virtual_cpu_count: int | None | Unset = UNSET





    def to_dict(self) -> dict[str, Any]:
        disk_mib: int | None | Unset
        if isinstance(self.disk_mib, Unset):
            disk_mib = UNSET
        else:
            disk_mib = self.disk_mib

        memory_mib: int | None | Unset
        if isinstance(self.memory_mib, Unset):
            memory_mib = UNSET
        else:
            memory_mib = self.memory_mib

        virtual_cpu_count: int | None | Unset
        if isinstance(self.virtual_cpu_count, Unset):
            virtual_cpu_count = UNSET
        else:
            virtual_cpu_count = self.virtual_cpu_count


        field_dict: dict[str, Any] = {}

        field_dict.update({
        })
        if disk_mib is not UNSET:
            field_dict["disk_mib"] = disk_mib
        if memory_mib is not UNSET:
            field_dict["memory_mib"] = memory_mib
        if virtual_cpu_count is not UNSET:
            field_dict["virtual_cpu_count"] = virtual_cpu_count

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        def _parse_disk_mib(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        disk_mib = _parse_disk_mib(d.pop("disk_mib", UNSET))


        def _parse_memory_mib(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        memory_mib = _parse_memory_mib(d.pop("memory_mib", UNSET))


        def _parse_virtual_cpu_count(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        virtual_cpu_count = _parse_virtual_cpu_count(d.pop("virtual_cpu_count", UNSET))


        memory_snapshot_configuration_payload = cls(
            disk_mib=disk_mib,
            memory_mib=memory_mib,
            virtual_cpu_count=virtual_cpu_count,
        )

        return memory_snapshot_configuration_payload

