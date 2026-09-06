package host

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

const (
	// memoryInfoPath is the kernel file that reports Linux memory information.
	memoryInfoPath = "/proc/meminfo"

	// bytesToMiBShift converts bytes to MiB.
	bytesToMiBShift = 20

	// kibibytesPerMiB converts the kB values in /proc/meminfo to MiB.
	kibibytesPerMiB = 1024
)

// memoryCapacityMiB returns total and available host memory.
func memoryCapacityMiB() (totalMiB, availableMiB int, err error) {
	var systemInformation syscall.Sysinfo_t
	if err := syscall.Sysinfo(&systemInformation); err != nil {
		return 0, 0, fmt.Errorf("read total memory: %w", err)
	}

	// Sysinfo reports memory in units of Unit bytes, not in bytes.
	unitBytes := uint64(systemInformation.Unit)
	if unitBytes == 0 {
		unitBytes = 1
	}
	totalMiB = int((uint64(systemInformation.Totalram) * unitBytes) >> bytesToMiBShift)

	availableMiB, err = readAvailableMemoryMiB()

	return totalMiB, availableMiB, err
}

// readAvailableMemoryMiB reads MemAvailable, which is what a new guest can use.
// Free memory alone understates it, because the kernel reclaims cache on demand.
func readAvailableMemoryMiB() (int, error) {
	file, err := os.Open(memoryInfoPath)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", memoryInfoPath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || fields[0] != "MemAvailable:" || fields[2] != "kB" {
			continue
		}

		kibibytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse available memory: %w", err)
		}

		return int(kibibytes / kibibytesPerMiB), nil
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read %s: %w", memoryInfoPath, err)
	}

	return 0, fmt.Errorf("MemAvailable is missing from %s", memoryInfoPath)
}
