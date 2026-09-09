from enum import StrEnum

class DownloadImageArtifact(StrEnum):
    KERNEL = "kernel"
    ROOTFS = "rootfs"

    def __str__(self) -> str:
        return str(self.value)
