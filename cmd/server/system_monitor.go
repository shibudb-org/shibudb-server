package server

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// CPUTimes holds CPU usage information
type CPUTimes struct {
	User   uint64
	Nice   uint64
	System uint64
	Idle   uint64
	IOWait uint64
	IRQ    uint64
	SoftIRQ uint64
	Steal  uint64
	Guest  uint64
	GuestNice uint64
}

// SystemMonitor provides system resource monitoring
type SystemMonitor struct {
	lastCPUTimes CPUTimes
	lastCPUTime  time.Time
}

// NewSystemMonitor creates a new system monitor
func NewSystemMonitor() *SystemMonitor {
	return &SystemMonitor{
		lastCPUTime: time.Now(),
	}
}

// GetCPUTimes reads CPU times from /proc/stat (Linux) or uses runtime info (other platforms)
func (sm *SystemMonitor) GetCPUTimes() (CPUTimes, error) {
	// Try to read from /proc/stat on Linux
	if runtime.GOOS == "linux" {
		return sm.readLinuxCPUTimes()
	}
	
	// Fallback for other platforms
	return sm.getFallbackCPUTimes(), nil
}

// readLinuxCPUTimes reads CPU times from /proc/stat
func (sm *SystemMonitor) readLinuxCPUTimes() (CPUTimes, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return CPUTimes{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) >= 11 {
				var times CPUTimes
				times.User, _ = strconv.ParseUint(fields[1], 10, 64)
				times.Nice, _ = strconv.ParseUint(fields[2], 10, 64)
				times.System, _ = strconv.ParseUint(fields[3], 10, 64)
				times.Idle, _ = strconv.ParseUint(fields[4], 10, 64)
				times.IOWait, _ = strconv.ParseUint(fields[5], 10, 64)
				times.IRQ, _ = strconv.ParseUint(fields[6], 10, 64)
				times.SoftIRQ, _ = strconv.ParseUint(fields[7], 10, 64)
				times.Steal, _ = strconv.ParseUint(fields[8], 10, 64)
				times.Guest, _ = strconv.ParseUint(fields[9], 10, 64)
				times.GuestNice, _ = strconv.ParseUint(fields[10], 10, 64)
				return times, nil
			}
		}
	}
	
	return CPUTimes{}, fmt.Errorf("could not parse /proc/stat")
}

// getFallbackCPUTimes provides basic CPU info for non-Linux platforms
func (sm *SystemMonitor) getFallbackCPUTimes() CPUTimes {
	// For non-Linux platforms, we'll use a simple approximation
	// based on goroutine count and runtime information
	return CPUTimes{
		User:   uint64(runtime.NumGoroutine() * 1000),
		System: uint64(runtime.NumCPU() * 500),
		Idle:   uint64(1000000 - runtime.NumGoroutine()*1000),
	}
}

// CalculateCPUUsage calculates CPU usage percentage
func (sm *SystemMonitor) CalculateCPUUsage() float64 {
	currentTimes, err := sm.GetCPUTimes()
	if err != nil {
		// Fallback calculation
		return sm.calculateFallbackCPUUsage()
	}

	if sm.lastCPUTime.IsZero() {
		sm.lastCPUTimes = currentTimes
		sm.lastCPUTime = time.Now()
		return 0.0
	}

	// Calculate total CPU time
	currentTotal := currentTimes.User + currentTimes.Nice + currentTimes.System + 
		currentTimes.Idle + currentTimes.IOWait + currentTimes.IRQ + 
		currentTimes.SoftIRQ + currentTimes.Steal + currentTimes.Guest + currentTimes.GuestNice
	
	lastTotal := sm.lastCPUTimes.User + sm.lastCPUTimes.Nice + sm.lastCPUTimes.System + 
		sm.lastCPUTimes.Idle + sm.lastCPUTimes.IOWait + sm.lastCPUTimes.IRQ + 
		sm.lastCPUTimes.SoftIRQ + sm.lastCPUTimes.Steal + sm.lastCPUTimes.Guest + sm.lastCPUTimes.GuestNice

	// Calculate idle time
	currentIdle := currentTimes.Idle + currentTimes.IOWait
	lastIdle := sm.lastCPUTimes.Idle + sm.lastCPUTimes.IOWait

	// Calculate usage
	totalDiff := currentTotal - lastTotal
	idleDiff := currentIdle - lastIdle

	if totalDiff == 0 {
		return 0.0
	}

	usage := 100.0 * (1.0 - float64(idleDiff)/float64(totalDiff))
	
	// Update last values
	sm.lastCPUTimes = currentTimes
	sm.lastCPUTime = time.Now()

	return usage
}

// calculateFallbackCPUUsage provides CPU usage estimation for platforms without /proc/stat
func (sm *SystemMonitor) calculateFallbackCPUUsage() float64 {
	now := time.Now()
	if sm.lastCPUTime.IsZero() {
		sm.lastCPUTime = now
		return 0.0
	}

	timeDiff := now.Sub(sm.lastCPUTime).Seconds()
	if timeDiff < 0.1 {
		return 0.0
	}

	// Estimate based on goroutine count and CPU cores
	goroutines := runtime.NumGoroutine()
	cpuCores := runtime.NumCPU()
	
	// Simple heuristic: more goroutines relative to CPU cores = higher usage
	usage := float64(goroutines) / float64(cpuCores*10) * 100.0
	
	if usage > 100.0 {
		usage = 100.0
	}

	sm.lastCPUTime = now
	return usage
}

// GetMemoryInfo returns detailed memory information
func GetMemoryInfo() map[string]interface{} {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	return map[string]interface{}{
		"alloc_bytes":      memStats.Alloc,
		"total_alloc_bytes": memStats.TotalAlloc,
		"sys_bytes":        memStats.Sys,
		"num_gc":           memStats.NumGC,
		"alloc_mb":         float64(memStats.Alloc) / 1024 / 1024,
		"sys_mb":           float64(memStats.Sys) / 1024 / 1024,
		"usage_mb":         float64(memStats.Alloc) / 1024 / 1024,
		"heap_alloc_mb":    float64(memStats.HeapAlloc) / 1024 / 1024,
		"heap_sys_mb":      float64(memStats.HeapSys) / 1024 / 1024,
		"heap_idle_mb":     float64(memStats.HeapIdle) / 1024 / 1024,
		"heap_inuse_mb":    float64(memStats.HeapInuse) / 1024 / 1024,
		"heap_released_mb": float64(memStats.HeapReleased) / 1024 / 1024,
		"heap_objects":     memStats.HeapObjects,
		"stack_inuse_mb":   float64(memStats.StackInuse) / 1024 / 1024,
		"stack_sys_mb":     float64(memStats.StackSys) / 1024 / 1024,
		"mspan_inuse_mb":   float64(memStats.MSpanInuse) / 1024 / 1024,
		"mspan_sys_mb":     float64(memStats.MSpanSys) / 1024 / 1024,
		"mcache_inuse_mb":  float64(memStats.MCacheInuse) / 1024 / 1024,
		"mcache_sys_mb":    float64(memStats.MCacheSys) / 1024 / 1024,
		"buck_hash_sys_mb": float64(memStats.BuckHashSys) / 1024 / 1024,
		"gc_sys_mb":        float64(memStats.GCSys) / 1024 / 1024,
		"other_sys_mb":     float64(memStats.OtherSys) / 1024 / 1024,
		"next_gc_mb":       float64(memStats.NextGC) / 1024 / 1024,
		"last_gc":          memStats.LastGC,
		"pause_total_ns":   memStats.PauseTotalNs,
		"pause_ns":         memStats.PauseNs[(memStats.NumGC+255)%256],
		"num_forced_gc":    memStats.NumForcedGC,
		"gc_cpu_fraction":  memStats.GCCPUFraction,
	}
}

// GetSystemInfo returns general system information
func GetSystemInfo() map[string]interface{} {
	return map[string]interface{}{
		"num_cpu":     runtime.NumCPU(),
		"goroutines":  runtime.NumGoroutine(),
		"go_version":  runtime.Version(),
		"go_os":       runtime.GOOS,
		"go_arch":     runtime.GOARCH,
		"compiler":    runtime.Compiler,
		"timestamp":   time.Now(),
	}
}
