# System Monitoring in ShibuDB

## Overview

ShibuDB now includes comprehensive system resource monitoring capabilities that provide real-time information about CPU usage, memory consumption, and other system metrics. This feature is integrated into the existing management API and provides both HTTP endpoints and CLI commands for monitoring.

## Features

- **Real-time CPU Usage**: Accurate CPU usage percentage calculation
- **Memory Monitoring**: Detailed memory allocation and usage statistics
- **Goroutine Tracking**: Active goroutine count monitoring
- **Cross-platform Support**: Works on Linux, macOS, and other platforms
- **HTTP API Integration**: RESTful endpoints for system monitoring
- **CLI Integration**: Command-line tools for easy monitoring

## Management API Endpoints

### Enhanced Stats Endpoint

**GET** `/stats` - Returns connection statistics with system information

```bash
curl http://localhost:10090/stats
```

Response:
```json
{
  "connections": {
    "active_connections": 45,
    "max_connections": 1000,
    "usage_percentage": 4.5,
    "available_slots": 955
  },
  "system": {
    "memory": {
      "alloc_mb": 25.6,
      "sys_mb": 45.2,
      "usage_mb": 25.6,
      "num_gc": 12
    },
    "cpu": {
      "num_cpu": 8,
      "usage_percent": 15.3
    },
    "goroutines": 23,
    "timestamp": "2024-01-15T10:30:45Z"
  }
}
```

### System Information Endpoint

**GET** `/system` - Returns detailed system resource information

```bash
curl http://localhost:10090/system
```

Response:
```json
{
  "memory": {
    "alloc_bytes": 268435456,
    "total_alloc_bytes": 1073741824,
    "sys_bytes": 473956352,
    "num_gc": 12,
    "alloc_mb": 25.6,
    "sys_mb": 45.2,
    "usage_mb": 25.6,
    "heap_alloc_mb": 20.1,
    "heap_sys_mb": 35.8,
    "heap_idle_mb": 15.7,
    "heap_inuse_mb": 20.1,
    "heap_released_mb": 0.0,
    "heap_objects": 125000,
    "stack_inuse_mb": 2.5,
    "stack_sys_mb": 3.2,
    "mspan_inuse_mb": 0.8,
    "mspan_sys_mb": 1.2,
    "mcache_inuse_mb": 0.1,
    "mcache_sys_mb": 0.2,
    "buck_hash_sys_mb": 0.5,
    "gc_sys_mb": 2.1,
    "other_sys_mb": 1.8,
    "next_gc_mb": 50.0,
    "last_gc": 1705311045000000000,
    "pause_total_ns": 15000000,
    "pause_ns": 500000,
    "num_forced_gc": 0,
    "gc_cpu_fraction": 0.02
  },
  "cpu": {
    "num_cpu": 8,
    "usage_percent": 15.3
  },
  "goroutines": 23,
  "timestamp": "2024-01-15T10:30:45Z"
}
```

## CLI Commands

### Enhanced Stats Command

```bash
shibudb manager 9090 stats
```

Output:
```
📊 Connection & System Statistics:
==================================
Active Connections: 45
Max Connections: 1000
Usage Percentage: 4.50%
Available Slots: 955

🖥️  System Resources:
Memory Usage: 25.60 MB
CPU Usage: 15.30% (8 cores)
Active Goroutines: 23
```

### System Information Command

```bash
shibudb manager 9090 system
```

Output:
```
🖥️  System Resource Information:
==================================
Memory Usage: 25.60 MB (Allocated: 25.60 MB, System: 45.20 MB)
Total Allocated: 1024.00 MB
Garbage Collections: 12
CPU Cores: 8
CPU Usage: 15.30%
Active Goroutines: 23
Timestamp: 2024-01-15T10:30:45Z
```

## Memory Metrics Explained

### Key Memory Metrics

- **Alloc (Allocated)**: Currently allocated heap memory
- **Sys (System)**: Total memory requested from the OS
- **Heap Alloc**: Heap memory currently in use
- **Heap Sys**: Total heap memory requested from the OS
- **Heap Idle**: Heap memory waiting to be released back to the OS
- **Heap Inuse**: Heap memory currently in use
- **Stack Inuse**: Stack memory currently in use
- **GC (Garbage Collection)**: Number of garbage collections performed

### Memory Usage Patterns

- **Low Memory Usage**: < 100 MB - Normal for idle server
- **Medium Memory Usage**: 100-500 MB - Active server with moderate load
- **High Memory Usage**: > 500 MB - Heavy load or potential memory leak

## CPU Usage Calculation

### Linux Systems

On Linux systems, CPU usage is calculated by reading `/proc/stat` and computing the difference between CPU time samples:

```go
usage = 100.0 * (1.0 - idle_time_diff / total_time_diff)
```

### Other Platforms

On non-Linux platforms, CPU usage is estimated based on:
- Number of active goroutines
- Number of CPU cores
- Time-based activity patterns

### CPU Usage Interpretation

- **0-20%**: Low usage, server is idle or lightly loaded
- **20-60%**: Normal usage, server is handling moderate load
- **60-80%**: High usage, server is under significant load
- **80-100%**: Very high usage, server may be overloaded

## Monitoring Best Practices

### Regular Monitoring

```bash
# Check system health every 5 minutes
while true; do
    shibudb manager 9090 system
    sleep 300
done
```

### Alerting Setup

```bash
#!/bin/bash
# Alert script for high resource usage

usage=$(shibudb manager 9090 system | grep "CPU Usage" | awk '{print $3}' | sed 's/%//')
memory=$(shibudb manager 9090 system | grep "Memory Usage" | awk '{print $3}' | sed 's/ MB//')

if (( $(echo "$usage > 80" | bc -l) )); then
    echo "ALERT: High CPU usage: ${usage}%"
fi

if (( $(echo "$memory > 500" | bc -l) )); then
    echo "ALERT: High memory usage: ${memory} MB"
fi
```

### Performance Monitoring

```bash
# Monitor performance over time
for i in {1..10}; do
    echo "=== Sample $i ==="
    shibudb manager 9090 stats
    sleep 10
done
```

## Integration with External Monitoring

### Prometheus Metrics

You can scrape the management API endpoints to collect metrics for Prometheus:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'shibudb'
    static_configs:
      - targets: ['localhost:10090']
    metrics_path: '/stats'
    scrape_interval: 15s
```

### Grafana Dashboard

Create a Grafana dashboard using the JSON API data source:

```json
{
  "targets": [
    {
      "refId": "A",
      "url": "http://localhost:10090/stats",
      "jsonPath": "$.system.cpu.usage_percent"
    }
  ]
}
```

## Troubleshooting

### High CPU Usage

1. **Check goroutine count**: High goroutine count may indicate goroutine leaks
2. **Monitor garbage collection**: Frequent GC may indicate memory pressure
3. **Review connection count**: High connection count may cause CPU spikes

### High Memory Usage

1. **Check heap allocation**: Monitor heap usage patterns
2. **Review garbage collection**: Check GC frequency and pause times
3. **Analyze memory leaks**: Look for continuously growing memory usage

### Performance Issues

1. **Monitor system resources**: Check CPU and memory usage
2. **Review connection limits**: Ensure limits are appropriate for your workload
3. **Check for bottlenecks**: Monitor response times and throughput

## Platform-Specific Notes

### Linux

- Most accurate CPU usage calculation using `/proc/stat`
- Full memory statistics available
- Best performance monitoring capabilities

### macOS

- CPU usage estimation based on goroutine activity
- Memory statistics available through Go runtime
- Good monitoring capabilities

### Windows

- CPU usage estimation based on runtime metrics
- Memory statistics available through Go runtime
- Basic monitoring capabilities

## Security Considerations

- Management API endpoints should be protected in production
- Use firewall rules to restrict access to management ports
- Consider using authentication for management endpoints
- Monitor access logs for unauthorized access attempts

## Future Enhancements

- **Disk I/O monitoring**: Track disk usage and I/O patterns
- **Network monitoring**: Monitor network connections and bandwidth
- **Process monitoring**: Track child processes and system calls
- **Custom metrics**: Allow custom metric collection and reporting
