from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast






T = TypeVar("T", bound="ImageResponse")



@_attrs_define
class ImageResponse:
    """ A tenant virtual machine image.

        Attributes:
            cache_image (bool):
            created_at (int):
            enabled (bool):
            id (str):
            image_type (str):
            kernel_size_mib (int):
            memory_snapshot (bool):
            operating_system (str):
            operating_system_version (str):
            platform (str):
            rootfs_size_mib (int):
            status (str):
            tenant_id (int):
            title (str):
            transfer_error (None | str):
            transfer_progress (int):
     """

    cache_image: bool
    created_at: int
    enabled: bool
    id: str
    image_type: str
    kernel_size_mib: int
    memory_snapshot: bool
    operating_system: str
    operating_system_version: str
    platform: str
    rootfs_size_mib: int
    status: str
    tenant_id: int
    title: str
    transfer_error: None | str
    transfer_progress: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        cache_image = self.cache_image

        created_at = self.created_at

        enabled = self.enabled

        id = self.id

        image_type = self.image_type

        kernel_size_mib = self.kernel_size_mib

        memory_snapshot = self.memory_snapshot

        operating_system = self.operating_system

        operating_system_version = self.operating_system_version

        platform = self.platform

        rootfs_size_mib = self.rootfs_size_mib

        status = self.status

        tenant_id = self.tenant_id

        title = self.title

        transfer_error: None | str
        transfer_error = self.transfer_error

        transfer_progress = self.transfer_progress


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "cache_image": cache_image,
            "created_at": created_at,
            "enabled": enabled,
            "id": id,
            "image_type": image_type,
            "kernel_size_mib": kernel_size_mib,
            "memory_snapshot": memory_snapshot,
            "operating_system": operating_system,
            "operating_system_version": operating_system_version,
            "platform": platform,
            "rootfs_size_mib": rootfs_size_mib,
            "status": status,
            "tenant_id": tenant_id,
            "title": title,
            "transfer_error": transfer_error,
            "transfer_progress": transfer_progress,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cache_image = d.pop("cache_image")

        created_at = d.pop("created_at")

        enabled = d.pop("enabled")

        id = d.pop("id")

        image_type = d.pop("image_type")

        kernel_size_mib = d.pop("kernel_size_mib")

        memory_snapshot = d.pop("memory_snapshot")

        operating_system = d.pop("operating_system")

        operating_system_version = d.pop("operating_system_version")

        platform = d.pop("platform")

        rootfs_size_mib = d.pop("rootfs_size_mib")

        status = d.pop("status")

        tenant_id = d.pop("tenant_id")

        title = d.pop("title")

        def _parse_transfer_error(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        transfer_error = _parse_transfer_error(d.pop("transfer_error"))


        transfer_progress = d.pop("transfer_progress")

        image_response = cls(
            cache_image=cache_image,
            created_at=created_at,
            enabled=enabled,
            id=id,
            image_type=image_type,
            kernel_size_mib=kernel_size_mib,
            memory_snapshot=memory_snapshot,
            operating_system=operating_system,
            operating_system_version=operating_system_version,
            platform=platform,
            rootfs_size_mib=rootfs_size_mib,
            status=status,
            tenant_id=tenant_id,
            title=title,
            transfer_error=transfer_error,
            transfer_progress=transfer_progress,
        )


        image_response.additional_properties = d
        return image_response

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
