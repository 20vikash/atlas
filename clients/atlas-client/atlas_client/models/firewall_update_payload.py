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





T = TypeVar("T", bound="FirewallUpdatePayload")



@_attrs_define
class FirewallUpdatePayload:
    """ Selected firewall fields to replace.

        Attributes:
            enabled (bool | None | Unset):
            inbound (list[FirewallRulePayload] | None | Unset):
            outbound (list[FirewallRulePayload] | None | Unset):
     """

    enabled: bool | None | Unset = UNSET
    inbound: list[FirewallRulePayload] | None | Unset = UNSET
    outbound: list[FirewallRulePayload] | None | Unset = UNSET





    def to_dict(self) -> dict[str, Any]:
        from ..models.firewall_rule_payload import FirewallRulePayload # noqa: PLC0415
        enabled: bool | None | Unset
        if isinstance(self.enabled, Unset):
            enabled = UNSET
        else:
            enabled = self.enabled

        inbound: list[dict[str, Any]] | None | Unset
        if isinstance(self.inbound, Unset):
            inbound = UNSET
        elif isinstance(self.inbound, list):
            inbound = []
            for inbound_type_0_item_data in self.inbound:
                inbound_type_0_item = inbound_type_0_item_data.to_dict()
                inbound.append(inbound_type_0_item)


        else:
            inbound = self.inbound

        outbound: list[dict[str, Any]] | None | Unset
        if isinstance(self.outbound, Unset):
            outbound = UNSET
        elif isinstance(self.outbound, list):
            outbound = []
            for outbound_type_0_item_data in self.outbound:
                outbound_type_0_item = outbound_type_0_item_data.to_dict()
                outbound.append(outbound_type_0_item)


        else:
            outbound = self.outbound


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
        def _parse_enabled(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        enabled = _parse_enabled(d.pop("enabled", UNSET))


        def _parse_inbound(data: object) -> list[FirewallRulePayload] | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                inbound_type_0 = []
                _inbound_type_0 = data
                for inbound_type_0_item_data in (_inbound_type_0):
                    inbound_type_0_item = FirewallRulePayload.from_dict(inbound_type_0_item_data)



                    inbound_type_0.append(inbound_type_0_item)

                return inbound_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[FirewallRulePayload] | None | Unset, data)

        inbound = _parse_inbound(d.pop("inbound", UNSET))


        def _parse_outbound(data: object) -> list[FirewallRulePayload] | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                outbound_type_0 = []
                _outbound_type_0 = data
                for outbound_type_0_item_data in (_outbound_type_0):
                    outbound_type_0_item = FirewallRulePayload.from_dict(outbound_type_0_item_data)



                    outbound_type_0.append(outbound_type_0_item)

                return outbound_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[FirewallRulePayload] | None | Unset, data)

        outbound = _parse_outbound(d.pop("outbound", UNSET))


        firewall_update_payload = cls(
            enabled=enabled,
            inbound=inbound,
            outbound=outbound,
        )

        return firewall_update_payload

