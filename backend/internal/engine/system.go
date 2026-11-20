package engine

import (
	"fmt"
	"runtime"
)

type systemStats struct {
	cpuModel        string
	cpuCores        int
	memoryTotal     uint64
	memoryAvailable uint64
	battery         string
}

func systemSummary(stats systemStats) string {
	cpu := stats.cpuModel
	if cpu == "" {
		cpu = "Unknown"
	}
	result := fmt.Sprintf("CPU: %s (%d logical cores)", cpu, stats.cpuCores)
	if stats.memoryTotal > 0 {
		used := stats.memoryTotal - stats.memoryAvailable
		result += fmt.Sprintf("\nRAM: %s used / %s total", formatBytes(used), formatBytes(stats.memoryTotal))
	} else {
		result += "\nRAM: unavailable"
	}
	battery := stats.battery
	if battery == "" {
		battery = "unavailable"
	}
	return result + "\nBattery: " + battery
}

func formatBytes(bytes uint64) string {
	const gib = 1024 * 1024 * 1024
	if bytes >= gib {
		return fmt.Sprintf("%.1f GiB", float64(bytes)/float64(gib))
	}
	return fmt.Sprintf("%.0f MiB", float64(bytes)/(1024*1024))
}

func baseStats() systemStats {
	return systemStats{cpuCores: runtime.NumCPU()}
}
