from enum import StrEnum

class PublicIPResponseStatus(StrEnum):
    ATTACHED = "attached"
    ATTACHING = "attaching"
    AVAILABLE = "available"
    DETACHING = "detaching"
    RESERVED = "reserved"

    def __str__(self) -> str:
        return str(self.value)
