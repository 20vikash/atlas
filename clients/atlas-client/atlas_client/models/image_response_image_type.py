from enum import StrEnum

class ImageResponseImageType(StrEnum):
    MACHINE = "machine"
    SYSTEM = "system"

    def __str__(self) -> str:
        return str(self.value)
