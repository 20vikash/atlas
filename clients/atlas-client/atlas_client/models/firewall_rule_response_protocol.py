from enum import StrEnum

class FirewallRuleResponseProtocol(StrEnum):
    ANY = "any"
    ICMP = "icmp"
    TCP = "tcp"
    UDP = "udp"

    def __str__(self) -> str:
        return str(self.value)
