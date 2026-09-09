from enum import StrEnum


class ReserveIPAddressPayloadSource(StrEnum):
    POOL = "pool"
    PROVIDER = "provider"

    def __str__(self) -> str:
        return str(self.value)
