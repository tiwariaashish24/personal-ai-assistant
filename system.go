package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// runPS executes a PowerShell command and returns trimmed stdout.
func runPS(script string) (string, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func systemInfo() error {
	fmt.Printf("OS:      %s %s/%s\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	if runtime.GOOS != "windows" {
		return nil
	}
	cpuName, _ := runPS("(Get-CimInstance Win32_Processor).Name")
	fmt.Printf("CPU:     %s\n", cpuName)
	ramInfo()
	return nil
}

func batteryInfo() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("battery info only supported on Windows")
	}
	out, err := runPS(`$b = Get-CimInstance Win32_Battery; if ($b) { "{0}% remaining, status: {1}" -f $b.EstimatedChargeRemaining, $b.BatteryStatus } else { "No battery detected" }`)
	if err != nil {
		return err
	}
	fmt.Println(out)
	Logf("battery: %s", out)
	return nil
}

func cpuInfo() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("cpu info only supported on Windows")
	}
	out, err := runPS(`(Get-Counter '\Processor(_Total)\% Processor Time').CounterSamples.CookedValue.ToString('F1') + '%'`)
	if err != nil {
		return err
	}
	fmt.Printf("CPU usage: %s\n", out)
	return nil
}

func ramInfo() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("ram info only supported on Windows")
	}
	out, err := runPS(`$os = Get-CimInstance Win32_OperatingSystem; $total = [math]::Round($os.TotalVisibleMemorySize/1MB,1); $free = [math]::Round($os.FreePhysicalMemory/1MB,1); "Total: {0} GB, Free: {1} GB, Used: {2} GB" -f $total, $free, ($total-$free)`)
	if err != nil {
		return err
	}
	fmt.Printf("RAM: %s\n", out)
	return nil
}
