from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.public_ip_response_delivery import PublicIPResponseDelivery
from ..models.public_ip_response_status import PublicIPResponseStatus
from ..models.public_ip_response_version import PublicIPResponseVersion
from typing import cast

if TYPE_CHECKING:
  from ..models.public_ip_response_tags import PublicIPResponseTags





T = TypeVar("T", bound="PublicIPResponse")



@_attrs_define
class PublicIPResponse:
    """ One tenant public IP.

        Attributes:
            created_at (int): Creation time as Unix seconds.
            delivery (PublicIPResponseDelivery): How traffic reaches the virtual machine.
            id (str): Public IP allocation ID.
            prefix (str): Allocated address or network in CIDR notation.
            reserved (bool): Whether the tenant keeps this allocation after detach.
            status (PublicIPResponseStatus): Current allocation lifecycle state.
            tags (PublicIPResponseTags): Resource tags as key-value pairs.
            tenant_id (int): Tenant that owns the allocation.
            version (PublicIPResponseVersion): IP protocol version.
            virtual_machine_id (None | str): Attached virtual machine ID, or null when detached.
     """

    created_at: int
    delivery: PublicIPResponseDelivery
    id: str
    prefix: str
    reserved: bool
    status: PublicIPResponseStatus
    tags: PublicIPResponseTags
    tenant_id: int
    version: PublicIPResponseVersion
    virtual_machine_id: None | str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.public_ip_response_tags import PublicIPResponseTags # noqa: PLC0415
        created_at = self.created_at

        delivery = self.delivery.value

        id = self.id

        prefix = self.prefix

        reserved = self.reserved

        status = self.status.value

        tags = self.tags.to_dict()

        tenant_id = self.tenant_id

        version = self.version.value

        virtual_machine_id: None | str
        virtual_machine_id = self.virtual_machine_id


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "created_at": created_at,
            "delivery": delivery,
            "id": id,
            "prefix": prefix,
            "reserved": reserved,
            "status": status,
            "tags": tags,
            "tenant_id": tenant_id,
            "version": version,
            "virtual_machine_id": virtual_machine_id,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.public_ip_response_tags import PublicIPResponseTags # noqa: PLC0415
        d = dict(src_dict)
        created_at = d.pop("created_at")

        delivery = PublicIPResponseDelivery(d.pop("delivery"))




        id = d.pop("id")

        prefix = d.pop("prefix")

        reserved = d.pop("reserved")

        status = PublicIPResponseStatus(d.pop("status"))




        tags = PublicIPResponseTags.from_dict(d.pop("tags"))




        tenant_id = d.pop("tenant_id")

        version = PublicIPResponseVersion(d.pop("version"))




        def _parse_virtual_machine_id(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        virtual_machine_id = _parse_virtual_machine_id(d.pop("virtual_machine_id"))


        public_ip_response = cls(
            created_at=created_at,
            delivery=delivery,
            id=id,
            prefix=prefix,
            reserved=reserved,
            status=status,
            tags=tags,
            tenant_id=tenant_id,
            version=version,
            virtual_machine_id=virtual_machine_id,
        )


        public_ip_response.additional_properties = d
        return public_ip_response

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
