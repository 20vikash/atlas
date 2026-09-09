from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ComputeUpdatePayload")


@_attrs_define
class ComputeUpdatePayload:
    """New CPU and memory values for a stopped virtual machine.

    Attributes:
        memory_mib (int | None | Unset):
        vcpus (int | None | Unset):
    """

    memory_mib: int | None | Unset = UNSET
    vcpus: int | None | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        memory_mib: int | None | Unset
        if isinstance(self.memory_mib, Unset):
            memory_mib = UNSET
        else:
            memory_mib = self.memory_mib

        vcpus: int | None | Unset
        if isinstance(self.vcpus, Unset):
            vcpus = UNSET
        else:
            vcpus = self.vcpus

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if memory_mib is not UNSET:
            field_dict["memory_mib"] = memory_mib
        if vcpus is not UNSET:
            field_dict["vcpus"] = vcpus

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_memory_mib(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        memory_mib = _parse_memory_mib(d.pop("memory_mib", UNSET))

        def _parse_vcpus(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        vcpus = _parse_vcpus(d.pop("vcpus", UNSET))

        compute_update_payload = cls(
            memory_mib=memory_mib,
            vcpus=vcpus,
        )

        return compute_update_payload
