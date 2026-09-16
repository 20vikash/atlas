from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.firewall_rule_response_protocol import FirewallRuleResponseProtocol
from typing import cast






T = TypeVar("T", bound="FirewallRuleResponse")



@_attrs_define
class FirewallRuleResponse:
    """ One desired firewall allow rule.

        Attributes:
            cidrs (list[str]):
            ports (str):
            protocol (FirewallRuleResponseProtocol):
     """

    cidrs: list[str]
    ports: str
    protocol: FirewallRuleResponseProtocol
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        cidrs = self.cidrs



        ports = self.ports

        protocol = self.protocol.value


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "cidrs": cidrs,
            "ports": ports,
            "protocol": protocol,
        })

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cidrs = cast(list[str], d.pop("cidrs"))


        ports = d.pop("ports")

        protocol = FirewallRuleResponseProtocol(d.pop("protocol"))




        firewall_rule_response = cls(
            cidrs=cidrs,
            ports=ports,
            protocol=protocol,
        )


        firewall_rule_response.additional_properties = d
        return firewall_rule_response

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
