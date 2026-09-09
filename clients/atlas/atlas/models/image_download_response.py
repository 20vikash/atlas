from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.image_download_response_artifact import ImageDownloadResponseArtifact

T = TypeVar("T", bound="ImageDownloadResponse")


@_attrs_define
class ImageDownloadResponse:
    """Signed downloads for one image.

    Attributes:
        artifact (ImageDownloadResponseArtifact):
        expires_at (int):
        expires_in (int):
        sha256 (str):
        size_mib (int):
        url (str):
    """

    artifact: ImageDownloadResponseArtifact
    expires_at: int
    expires_in: int
    sha256: str
    size_mib: int
    url: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        artifact = self.artifact.value

        expires_at = self.expires_at

        expires_in = self.expires_in

        sha256 = self.sha256

        size_mib = self.size_mib

        url = self.url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "artifact": artifact,
                "expires_at": expires_at,
                "expires_in": expires_in,
                "sha256": sha256,
                "size_mib": size_mib,
                "url": url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        artifact = ImageDownloadResponseArtifact(d.pop("artifact"))

        expires_at = d.pop("expires_at")

        expires_in = d.pop("expires_in")

        sha256 = d.pop("sha256")

        size_mib = d.pop("size_mib")

        url = d.pop("url")

        image_download_response = cls(
            artifact=artifact,
            expires_at=expires_at,
            expires_in=expires_in,
            sha256=sha256,
            size_mib=size_mib,
            url=url,
        )

        image_download_response.additional_properties = d
        return image_download_response

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
