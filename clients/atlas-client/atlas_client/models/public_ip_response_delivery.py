from enum import StrEnum

class PublicIPResponseDelivery(StrEnum):
    DIRECT = "direct"
    ROUTED = "routed"

    def __str__(self) -> str:
        return str(self.value)
