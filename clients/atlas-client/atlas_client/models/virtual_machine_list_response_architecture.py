from enum import StrEnum

class VirtualMachineListResponseArchitecture(StrEnum):
    AMD64 = "amd64"
    ARM64 = "arm64"

    def __str__(self) -> str:
        return str(self.value)
