from enum import StrEnum


class ConsoleTokenResponseMode(StrEnum):
    SSH = "ssh"
    TTY = "tty"

    def __str__(self) -> str:
        return str(self.value)
