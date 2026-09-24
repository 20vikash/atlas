from enum import StrEnum

class ListPublicIpsVersionType0(StrEnum):
    VALUE_0 = "4"
    VALUE_1 = "6"

    def __str__(self) -> str:
        return str(self.value)
