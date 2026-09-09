from enum import StrEnum


class NetworkUpdatePayloadEgressType0(StrEnum):
    MESH = "mesh"
    NONE = "none"
    UPLINK = "uplink"

    def __str__(self) -> str:
        return str(self.value)
