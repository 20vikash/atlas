from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="SnapshotPayload")


@_attrs_define
class SnapshotPayload:
    """Values that create one Machine image from a virtual machine.

    Attributes:
        title (str):
        cache_image (bool | Unset):  Default: False.
        memory_snapshot (bool | Unset):  Default: False.
    """

    title: str
    cache_image: bool | Unset = False
    memory_snapshot: bool | Unset = False

    def to_dict(self) -> dict[str, Any]:
        title = self.title

        cache_image = self.cache_image

        memory_snapshot = self.memory_snapshot

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "title": title,
            }
        )
        if cache_image is not UNSET:
            field_dict["cache_image"] = cache_image
        if memory_snapshot is not UNSET:
            field_dict["memory_snapshot"] = memory_snapshot

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        title = d.pop("title")

        cache_image = d.pop("cache_image", UNSET)

        memory_snapshot = d.pop("memory_snapshot", UNSET)

        snapshot_payload = cls(
            title=title,
            cache_image=cache_image,
            memory_snapshot=memory_snapshot,
        )

        return snapshot_payload
