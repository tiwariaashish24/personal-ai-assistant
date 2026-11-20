//go:build linux

package engine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func platformStats() systemStats {
	stats := baseStats()
	if contents, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(contents), "\n") {
			if strings.HasPrefix(line, "model name") {
				if _, value, ok := strings.Cut(line, ":"); ok {
					stats.cpuModel = strings.TrimSpace(value)
				}
				break
			}
		}
	}
	if contents, err := os.ReadFile("/proc/meminfo"); err == nil {
		var total, available uint64
		for _, line := range strings.Split(string(contents), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				continue
			}
			switch fields[0] {
			case "MemTotal:":
				total = value * 1024
			case "MemAvailable:":
				available = value * 1024
			}
		}
		stats.memoryTotal, stats.memoryAvailable = total, available
	}
	stats.battery = linuxBattery()
	return stats
}

func linuxBattery() string {
	devices, _ := filepath.Glob("/sys/class/power_supply/*")
	for _, device := range devices {
		kind, err := os.ReadFile(filepath.Join(device, "type"))
		if err != nil || strings.TrimSpace(string(kind)) != "Battery" {
			continue
		}
		capacity, err := os.ReadFile(filepath.Join(device, "capacity"))
		if err == nil {
			return strings.TrimSpace(string(capacity)) + "%"
		}
	}
	return "not detected"
}
