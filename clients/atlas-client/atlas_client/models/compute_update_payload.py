from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast






T = TypeVar("T", bound="ComputeUpdatePayload")



@_attrs_define
class ComputeUpdatePayload:
    """ New compute configuration.

        Attributes:
            memory_mib (int | None | Unset):
            sleep_after_idle_seconds (int | None | Unset):
            vcpus (int | None | Unset):
     """

    memory_mib: int | None | Unset = UNSET
    sleep_after_idle_seconds: int | None | Unset = UNSET
    vcpus: int | None | Unset = UNSET





    def to_dict(self) -> dict[str, Any]:
        memory_mib: int | None | Unset
        if isinstance(self.memory_mib, Unset):
            memory_mib = UNSET
        else:
            memory_mib = self.memory_mib

        sleep_after_idle_seconds: int | None | Unset
        if isinstance(self.sleep_after_idle_seconds, Unset):
            sleep_after_idle_seconds = UNSET
        else:
            sleep_after_idle_seconds = self.sleep_after_idle_seconds

        vcpus: int | None | Unset
        if isinstance(self.vcpus, Unset):
            vcpus = UNSET
        else:
            vcpus = self.vcpus


        field_dict: dict[str, Any] = {}

        field_dict.update({
        })
        if memory_mib is not UNSET:
            field_dict["memory_mib"] = memory_mib
        if sleep_after_idle_seconds is not UNSET:
            field_dict["sleep_after_idle_seconds"] = sleep_after_idle_seconds
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


        def _parse_sleep_after_idle_seconds(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        sleep_after_idle_seconds = _parse_sleep_after_idle_seconds(d.pop("sleep_after_idle_seconds", UNSET))


        def _parse_vcpus(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        vcpus = _parse_vcpus(d.pop("vcpus", UNSET))


        compute_update_payload = cls(
            memory_mib=memory_mib,
            sleep_after_idle_seconds=sleep_after_idle_seconds,
            vcpus=vcpus,
        )

        return compute_update_payload

