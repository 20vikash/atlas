from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast

if TYPE_CHECKING:
  from ..models.firewall_rule_payload import FirewallRulePayload





T = TypeVar("T", bound="FirewallPayload")



@_attrs_define
class FirewallPayload:
    """ The complete desired firewall configuration.

        Attributes:
            enabled (bool | Unset):  Default: False.
            inbound (list[FirewallRulePayload] | Unset):
            outbound (list[FirewallRulePayload] | Unset):
     """

    enabled: bool | Unset = False
    inbound: list[FirewallRulePayload] | Unset = UNSET
    outbound: list[FirewallRulePayload] | Unset = UNSET





    def to_dict(self) -> dict[str, Any]:
        from ..models.firewall_rule_payload import FirewallRulePayload # noqa: PLC0415
        enabled = self.enabled

        inbound: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.inbound, Unset):
            inbound = []
            for inbound_item_data in self.inbound:
                inbound_item = inbound_item_data.to_dict()
                inbound.append(inbound_item)



        outbound: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.outbound, Unset):
            outbound = []
            for outbound_item_data in self.outbound:
                outbound_item = outbound_item_data.to_dict()
                outbound.append(outbound_item)




        field_dict: dict[str, Any] = {}

        field_dict.update({
        })
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if inbound is not UNSET:
            field_dict["inbound"] = inbound
        if outbound is not UNSET:
            field_dict["outbound"] = outbound

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.firewall_rule_payload import FirewallRulePayload # noqa: PLC0415
        d = dict(src_dict)
        enabled = d.pop("enabled", UNSET)

        _inbound = d.pop("inbound", UNSET)
        inbound: list[FirewallRulePayload] | Unset = UNSET
        if _inbound is not UNSET:
            inbound = []
            for inbound_item_data in _inbound:
                inbound_item = FirewallRulePayload.from_dict(inbound_item_data)



                inbound.append(inbound_item)


        _outbound = d.pop("outbound", UNSET)
        outbound: list[FirewallRulePayload] | Unset = UNSET
        if _outbound is not UNSET:
            outbound = []
            for outbound_item_data in _outbound:
                outbound_item = FirewallRulePayload.from_dict(outbound_item_data)



                outbound.append(outbound_item)


        firewall_payload = cls(
            enabled=enabled,
            inbound=inbound,
            outbound=outbound,
        )

        return firewall_payload

