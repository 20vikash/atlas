from enum import StrEnum

class ListImagesImageTypeType0(StrEnum):
    MACHINE = "machine"
    SYSTEM = "system"

    def __str__(self) -> str:
        return str(self.value)
