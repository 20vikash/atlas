from enum import IntEnum

class PublicIPResponseVersion(IntEnum):
    VALUE_4 = 4
    VALUE_6 = 6

    def __str__(self) -> str:
        return str(self.value)
