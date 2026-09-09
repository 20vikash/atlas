from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast






T = TypeVar("T", bound="DiskUpdatePayload")



@_attrs_define
class DiskUpdatePayload:
    """ New disk size and disk rate limits.

        Attributes:
            disk_iops (int | None | Unset):
            disk_mib (int | None | Unset):
            disk_throughput_mibps (int | None | Unset):
     """

    disk_iops: int | None | Unset = UNSET
    disk_mib: int | None | Unset = UNSET
    disk_throughput_mibps: int | None | Unset = UNSET





    def to_dict(self) -> dict[str, Any]:
        disk_iops: int | None | Unset
        if isinstance(self.disk_iops, Unset):
            disk_iops = UNSET
        else:
            disk_iops = self.disk_iops

        disk_mib: int | None | Unset
        if isinstance(self.disk_mib, Unset):
            disk_mib = UNSET
        else:
            disk_mib = self.disk_mib

        disk_throughput_mibps: int | None | Unset
        if isinstance(self.disk_throughput_mibps, Unset):
            disk_throughput_mibps = UNSET
        else:
            disk_throughput_mibps = self.disk_throughput_mibps


        field_dict: dict[str, Any] = {}

        field_dict.update({
        })
        if disk_iops is not UNSET:
            field_dict["disk_iops"] = disk_iops
        if disk_mib is not UNSET:
            field_dict["disk_mib"] = disk_mib
        if disk_throughput_mibps is not UNSET:
            field_dict["disk_throughput_mibps"] = disk_throughput_mibps

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        def _parse_disk_iops(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        disk_iops = _parse_disk_iops(d.pop("disk_iops", UNSET))


        def _parse_disk_mib(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        disk_mib = _parse_disk_mib(d.pop("disk_mib", UNSET))


        def _parse_disk_throughput_mibps(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        disk_throughput_mibps = _parse_disk_throughput_mibps(d.pop("disk_throughput_mibps", UNSET))


        disk_update_payload = cls(
            disk_iops=disk_iops,
            disk_mib=disk_mib,
            disk_throughput_mibps=disk_throughput_mibps,
        )

        return disk_update_payload

