//go:build windows

package engine

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	globalMemoryStatusExProc = kernel32.NewProc("GlobalMemoryStatusEx")
	getSystemPowerStatusProc = kernel32.NewProc("GetSystemPowerStatus")
)

type memoryStatusEx struct {
	length                uint32
	memoryLoad            uint32
	totalPhys             uint64
	availablePhys         uint64
	totalPageFile         uint64
	availablePageFile     uint64
	totalVirtual          uint64
	availableVirtual      uint64
	availableExtendedVirt uint64
}

type systemPowerStatus struct {
	acLineStatus        byte
	batteryFlag         byte
	batteryLifePercent  byte
	systemStatusFlag    byte
	batteryLifeTime     uint32
	batteryFullLifeTime uint32
}

func platformStats() systemStats {
	stats := baseStats()
	stats.cpuModel = strings.TrimSpace(os.Getenv("PROCESSOR_IDENTIFIER"))

	memory := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if result, _, _ := globalMemoryStatusExProc.Call(uintptr(unsafe.Pointer(&memory))); result != 0 {
		stats.memoryTotal = memory.totalPhys
		stats.memoryAvailable = memory.availablePhys
	}

	power := systemPowerStatus{}
	if result, _, _ := getSystemPowerStatusProc.Call(uintptr(unsafe.Pointer(&power))); result != 0 {
		if power.batteryFlag == 128 {
			stats.battery = "not detected"
		} else if power.batteryLifePercent <= 100 {
			stats.battery = formatPercent(power.batteryLifePercent)
		} else {
			stats.battery = "unknown"
		}
	}
	return stats
}

func formatPercent(value byte) string {
	if value > 100 {
		return "unknown"
	}
	return fmt.Sprintf("%d%%", value)
}
