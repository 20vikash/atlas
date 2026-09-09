from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast

if TYPE_CHECKING:
  from ..models.metadata_replacement_payload_metadata import MetadataReplacementPayloadMetadata





T = TypeVar("T", bound="MetadataReplacementPayload")



@_attrs_define
class MetadataReplacementPayload:
    """ The complete custom metadata map.

        Attributes:
            metadata (MetadataReplacementPayloadMetadata):
     """

    metadata: MetadataReplacementPayloadMetadata





    def to_dict(self) -> dict[str, Any]:
        from ..models.metadata_replacement_payload_metadata import MetadataReplacementPayloadMetadata # noqa: PLC0415
        metadata = self.metadata.to_dict()


        field_dict: dict[str, Any] = {}

        field_dict.update({
            "metadata": metadata,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.metadata_replacement_payload_metadata import MetadataReplacementPayloadMetadata # noqa: PLC0415
        d = dict(src_dict)
        metadata = MetadataReplacementPayloadMetadata.from_dict(d.pop("metadata"))




        metadata_replacement_payload = cls(
            metadata=metadata,
        )

        return metadata_replacement_payload

