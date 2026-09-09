from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.create_virtual_machine_payload_egress import CreateVirtualMachinePayloadEgress
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_virtual_machine_payload_metadata import CreateVirtualMachinePayloadMetadata


T = TypeVar("T", bound="CreateVirtualMachinePayload")


@_attrs_define
class CreateVirtualMachinePayload:
    """Values that create one virtual machine.

    Attributes:
        disk_mib (int):
        image_id (str):
        memory_mib (int):
        vcpus (int):
        disk_iops (int | Unset):  Default: 0.
        disk_throughput_mibps (int | Unset):  Default: 0.
        egress (CreateVirtualMachinePayloadEgress | Unset):  Default: CreateVirtualMachinePayloadEgress.UPLINK.
        hostname (str | Unset):  Default: ''.
        idle_timeout_seconds (int | Unset):  Default: 0.
        ip_address_id (None | str | Unset):
        is_sleepy (bool | Unset):  Default: False.
        metadata (CreateVirtualMachinePayloadMetadata | Unset):
        private_network_throughput_mibps (int | Unset):  Default: 0.
        public_network_throughput_mibps (int | Unset):  Default: 0.
        ssh_keys (list[str] | Unset):
        user_data (str | Unset):  Default: ''.
    """

    disk_mib: int
    image_id: str
    memory_mib: int
    vcpus: int
    disk_iops: int | Unset = 0
    disk_throughput_mibps: int | Unset = 0
    egress: CreateVirtualMachinePayloadEgress | Unset = CreateVirtualMachinePayloadEgress.UPLINK
    hostname: str | Unset = ""
    idle_timeout_seconds: int | Unset = 0
    ip_address_id: None | str | Unset = UNSET
    is_sleepy: bool | Unset = False
    metadata: CreateVirtualMachinePayloadMetadata | Unset = UNSET
    private_network_throughput_mibps: int | Unset = 0
    public_network_throughput_mibps: int | Unset = 0
    ssh_keys: list[str] | Unset = UNSET
    user_data: str | Unset = ""

    def to_dict(self) -> dict[str, Any]:
        disk_mib = self.disk_mib

        image_id = self.image_id

        memory_mib = self.memory_mib

        vcpus = self.vcpus

        disk_iops = self.disk_iops

        disk_throughput_mibps = self.disk_throughput_mibps

        egress: str | Unset = UNSET
        if not isinstance(self.egress, Unset):
            egress = self.egress.value

        hostname = self.hostname

        idle_timeout_seconds = self.idle_timeout_seconds

        ip_address_id: None | str | Unset
        if isinstance(self.ip_address_id, Unset):
            ip_address_id = UNSET
        else:
            ip_address_id = self.ip_address_id

        is_sleepy = self.is_sleepy

        metadata: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        private_network_throughput_mibps = self.private_network_throughput_mibps

        public_network_throughput_mibps = self.public_network_throughput_mibps

        ssh_keys: list[str] | Unset = UNSET
        if not isinstance(self.ssh_keys, Unset):
            ssh_keys = self.ssh_keys

        user_data = self.user_data

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "disk_mib": disk_mib,
                "image_id": image_id,
                "memory_mib": memory_mib,
                "vcpus": vcpus,
            }
        )
        if disk_iops is not UNSET:
            field_dict["disk_iops"] = disk_iops
        if disk_throughput_mibps is not UNSET:
            field_dict["disk_throughput_mibps"] = disk_throughput_mibps
        if egress is not UNSET:
            field_dict["egress"] = egress
        if hostname is not UNSET:
            field_dict["hostname"] = hostname
        if idle_timeout_seconds is not UNSET:
            field_dict["idle_timeout_seconds"] = idle_timeout_seconds
        if ip_address_id is not UNSET:
            field_dict["ip_address_id"] = ip_address_id
        if is_sleepy is not UNSET:
            field_dict["is_sleepy"] = is_sleepy
        if metadata is not UNSET:
            field_dict["metadata"] = metadata
        if private_network_throughput_mibps is not UNSET:
            field_dict["private_network_throughput_mibps"] = private_network_throughput_mibps
        if public_network_throughput_mibps is not UNSET:
            field_dict["public_network_throughput_mibps"] = public_network_throughput_mibps
        if ssh_keys is not UNSET:
            field_dict["ssh_keys"] = ssh_keys
        if user_data is not UNSET:
            field_dict["user_data"] = user_data

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_virtual_machine_payload_metadata import (
            CreateVirtualMachinePayloadMetadata,  # noqa: PLC0415
        )

        d = dict(src_dict)
        disk_mib = d.pop("disk_mib")

        image_id = d.pop("image_id")

        memory_mib = d.pop("memory_mib")

        vcpus = d.pop("vcpus")

        disk_iops = d.pop("disk_iops", UNSET)

        disk_throughput_mibps = d.pop("disk_throughput_mibps", UNSET)

        _egress = d.pop("egress", UNSET)
        egress: CreateVirtualMachinePayloadEgress | Unset
        if isinstance(_egress, Unset):
            egress = UNSET
        else:
            egress = CreateVirtualMachinePayloadEgress(_egress)

        hostname = d.pop("hostname", UNSET)

        idle_timeout_seconds = d.pop("idle_timeout_seconds", UNSET)

        def _parse_ip_address_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        ip_address_id = _parse_ip_address_id(d.pop("ip_address_id", UNSET))

        is_sleepy = d.pop("is_sleepy", UNSET)

        _metadata = d.pop("metadata", UNSET)
        metadata: CreateVirtualMachinePayloadMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = CreateVirtualMachinePayloadMetadata.from_dict(_metadata)

        private_network_throughput_mibps = d.pop("private_network_throughput_mibps", UNSET)

        public_network_throughput_mibps = d.pop("public_network_throughput_mibps", UNSET)

        ssh_keys = cast(list[str], d.pop("ssh_keys", UNSET))

        user_data = d.pop("user_data", UNSET)

        create_virtual_machine_payload = cls(
            disk_mib=disk_mib,
            image_id=image_id,
            memory_mib=memory_mib,
            vcpus=vcpus,
            disk_iops=disk_iops,
            disk_throughput_mibps=disk_throughput_mibps,
            egress=egress,
            hostname=hostname,
            idle_timeout_seconds=idle_timeout_seconds,
            ip_address_id=ip_address_id,
            is_sleepy=is_sleepy,
            metadata=metadata,
            private_network_throughput_mibps=private_network_throughput_mibps,
            public_network_throughput_mibps=public_network_throughput_mibps,
            ssh_keys=ssh_keys,
            user_data=user_data,
        )

        return create_virtual_machine_payload
