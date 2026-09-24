from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.image_response_architecture import ImageResponseArchitecture
from ..models.image_response_image_type import ImageResponseImageType
from ..models.image_response_status import ImageResponseStatus
from typing import cast

if TYPE_CHECKING:
  from ..models.image_response_tags import ImageResponseTags





T = TypeVar("T", bound="ImageResponse")



@_attrs_define
class ImageResponse:
    """ A tenant virtual machine image.

        Attributes:
            architecture (ImageResponseArchitecture): CPU architecture.
            cache_image (bool): Whether hosts may keep this image cached.
            created_at (int): Creation time as Unix seconds.
            enabled (bool): Whether the image can create a virtual machine.
            id (str): Virtual machine image ID.
            image_type (ImageResponseImageType): System image or tenant machine snapshot.
            is_termination_protected (bool): Whether deletion is blocked.
            kernel_size_mib (int): Kernel artifact size in MiB.
            memory_snapshot (bool): Whether the image includes guest memory.
            rootfs_size_mib (int): Root filesystem size in MiB.
            status (ImageResponseStatus): Current image lifecycle state.
            tags (ImageResponseTags): Resource tags as key-value pairs.
            tenant_id (int): Tenant that owns the image.
            title (str): Display title.
            transfer_error (None | str): Last transfer error, or null.
            transfer_progress (int): Transfer completion percentage.
     """

    architecture: ImageResponseArchitecture
    cache_image: bool
    created_at: int
    enabled: bool
    id: str
    image_type: ImageResponseImageType
    is_termination_protected: bool
    kernel_size_mib: int
    memory_snapshot: bool
    rootfs_size_mib: int
    status: ImageResponseStatus
    tags: ImageResponseTags
    tenant_id: int
    title: str
    transfer_error: None | str
    transfer_progress: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.image_response_tags import ImageResponseTags # noqa: PLC0415
        architecture = self.architecture.value

        cache_image = self.cache_image

        created_at = self.created_at

        enabled = self.enabled

        id = self.id

        image_type = self.image_type.value

        is_termination_protected = self.is_termination_protected

        kernel_size_mib = self.kernel_size_mib

        memory_snapshot = self.memory_snapshot

        rootfs_size_mib = self.rootfs_size_mib

        status = self.status.value

        tags = self.tags.to_dict()

        tenant_id = self.tenant_id

        title = self.title

        transfer_error: None | str
        transfer_error = self.transfer_error

        transfer_progress = self.transfer_progress


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "architecture": architecture,
            "cache_image": cache_image,
            "created_at": created_at,
            "enabled": enabled,
            "id": id,
            "image_type": image_type,
            "is_termination_protected": is_termination_protected,
            "kernel_size_mib": kernel_size_mib,
            "memory_snapshot": memory_snapshot,
            "rootfs_size_mib": rootfs_size_mib,
            "status": status,
            "tags": tags,
            "tenant_id": tenant_id,
            "title": title,
            "transfer_error": transfer_error,
            "transfer_progress": transfer_progress,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.image_response_tags import ImageResponseTags # noqa: PLC0415
        d = dict(src_dict)
        architecture = ImageResponseArchitecture(d.pop("architecture"))




        cache_image = d.pop("cache_image")

        created_at = d.pop("created_at")

        enabled = d.pop("enabled")

        id = d.pop("id")

        image_type = ImageResponseImageType(d.pop("image_type"))




        is_termination_protected = d.pop("is_termination_protected")

        kernel_size_mib = d.pop("kernel_size_mib")

        memory_snapshot = d.pop("memory_snapshot")

        rootfs_size_mib = d.pop("rootfs_size_mib")

        status = ImageResponseStatus(d.pop("status"))




        tags = ImageResponseTags.from_dict(d.pop("tags"))




        tenant_id = d.pop("tenant_id")

        title = d.pop("title")

        def _parse_transfer_error(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        transfer_error = _parse_transfer_error(d.pop("transfer_error"))


        transfer_progress = d.pop("transfer_progress")

        image_response = cls(
            architecture=architecture,
            cache_image=cache_image,
            created_at=created_at,
            enabled=enabled,
            id=id,
            image_type=image_type,
            is_termination_protected=is_termination_protected,
            kernel_size_mib=kernel_size_mib,
            memory_snapshot=memory_snapshot,
            rootfs_size_mib=rootfs_size_mib,
            status=status,
            tags=tags,
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
