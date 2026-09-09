from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.network_update_payload_egress_type_0 import NetworkUpdatePayloadEgressType0
from ..types import UNSET, Unset

T = TypeVar("T", bound="NetworkUpdatePayload")


@_attrs_define
class NetworkUpdatePayload:
    """New egress mode and network rate limits.

    Attributes:
        egress (NetworkUpdatePayloadEgressType0 | None | Unset):
        private_network_throughput_mibps (int | None | Unset):
        public_network_throughput_mibps (int | None | Unset):
    """

    egress: NetworkUpdatePayloadEgressType0 | None | Unset = UNSET
    private_network_throughput_mibps: int | None | Unset = UNSET
    public_network_throughput_mibps: int | None | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        egress: None | str | Unset
        if isinstance(self.egress, Unset):
            egress = UNSET
        elif isinstance(self.egress, NetworkUpdatePayloadEgressType0):
            egress = self.egress.value
        else:
            egress = self.egress

        private_network_throughput_mibps: int | None | Unset
        if isinstance(self.private_network_throughput_mibps, Unset):
            private_network_throughput_mibps = UNSET
        else:
            private_network_throughput_mibps = self.private_network_throughput_mibps

        public_network_throughput_mibps: int | None | Unset
        if isinstance(self.public_network_throughput_mibps, Unset):
            public_network_throughput_mibps = UNSET
        else:
            public_network_throughput_mibps = self.public_network_throughput_mibps

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if egress is not UNSET:
            field_dict["egress"] = egress
        if private_network_throughput_mibps is not UNSET:
            field_dict["private_network_throughput_mibps"] = private_network_throughput_mibps
        if public_network_throughput_mibps is not UNSET:
            field_dict["public_network_throughput_mibps"] = public_network_throughput_mibps

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_egress(data: object) -> NetworkUpdatePayloadEgressType0 | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                egress_type_0 = NetworkUpdatePayloadEgressType0(data)

                return egress_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(NetworkUpdatePayloadEgressType0 | None | Unset, data)

        egress = _parse_egress(d.pop("egress", UNSET))

        def _parse_private_network_throughput_mibps(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        private_network_throughput_mibps = _parse_private_network_throughput_mibps(
            d.pop("private_network_throughput_mibps", UNSET)
        )

        def _parse_public_network_throughput_mibps(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        public_network_throughput_mibps = _parse_public_network_throughput_mibps(
            d.pop("public_network_throughput_mibps", UNSET)
        )

        network_update_payload = cls(
            egress=egress,
            private_network_throughput_mibps=private_network_throughput_mibps,
            public_network_throughput_mibps=public_network_throughput_mibps,
        )

        return network_update_payload
