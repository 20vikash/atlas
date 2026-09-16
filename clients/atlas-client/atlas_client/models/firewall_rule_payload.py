from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.firewall_rule_payload_protocol import FirewallRulePayloadProtocol
from ..types import UNSET, Unset
from typing import cast






T = TypeVar("T", bound="FirewallRulePayload")



@_attrs_define
class FirewallRulePayload:
    """ One firewall allow rule.

        Attributes:
            cidrs (list[str]):
            protocol (FirewallRulePayloadProtocol):
            ports (str | Unset):  Default: ''.
     """

    cidrs: list[str]
    protocol: FirewallRulePayloadProtocol
    ports: str | Unset = ''





    def to_dict(self) -> dict[str, Any]:
        cidrs = self.cidrs



        protocol = self.protocol.value

        ports = self.ports


        field_dict: dict[str, Any] = {}

        field_dict.update({
            "cidrs": cidrs,
            "protocol": protocol,
        })
        if ports is not UNSET:
            field_dict["ports"] = ports

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cidrs = cast(list[str], d.pop("cidrs"))


        protocol = FirewallRulePayloadProtocol(d.pop("protocol"))




        ports = d.pop("ports", UNSET)

        firewall_rule_payload = cls(
            cidrs=cidrs,
            protocol=protocol,
            ports=ports,
        )

        return firewall_rule_payload

