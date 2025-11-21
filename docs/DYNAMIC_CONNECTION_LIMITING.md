# Dynamic Connection Limiting in ShibuDB

## Overview

ShibuDB now supports **dynamic connection limiting** that allows you to update connection limits at runtime without restarting the server. This feature provides multiple ways to manage connection limits based on your operational needs.

## Key Features

- **Zero Downtime Updates**: Change connection limits without restarting the server
- **Multiple Management Interfaces**: HTTP API, CLI tool, and Unix signals
- **Safety Checks**: Prevents setting limits below active connections
- **Real-time Monitoring**: Live statistics and health checks
- **Thread-safe Operations**: Atomic updates with proper synchronization

## Management Interfaces

### 1. HTTP Management API

The server automatically starts a management HTTP server on port `<main_port> + 1000`.

#### Endpoints

**Health Check**
```bash
GET http://localhost:10090/health
```
Response:
```json
{
  "status": "healthy",
  "service": "shibudb"
}
```

**Connection Statistics**
```bash
GET http://localhost:10090/stats
```
Response:
```json
{
  "active_connections": 45,
  "max_connections": 1000,
  "usage_percentage": 4.5,
  "available_slots": 955
}
```

**Get Current Limit**
```bash
GET http://localhost:10090/limit
```
Response:
```json
{
  "current_limit": 1000,
  "active_connections": 45
}
```

**Set Connection Limit**
```bash
PUT http://localhost:10090/limit
Content-Type: application/json

{
  "limit": 2000
}
```
Response:
```json
{
  "status": "success",
  "new_limit": 2000,
  "message": "Connection limit updated to 2000"
}
```

**Increase Limit**
```bash
POST http://localhost:10090/limit/increase
Content-Type: application/json

{
  "amount": 500
}
```
Response:
```json
{
  "status": "success",
  "old_limit": 1000,
  "new_limit": 1500,
  "increase_amount": 500,
  "message": "Connection limit increased from 1000 to 1500"
}
```

**Decrease Limit**
```bash
POST http://localhost:10090/limit/decrease
Content-Type: application/json

{
  "amount": 200
}
```
Response:
```json
{
  "status": "success",
  "old_limit": 1500,
  "new_limit": 1300,
  "decrease_amount": 200,
  "message": "Connection limit decreased from 1500 to 1300"
}
```

### 2. CLI Management Tool

Use the built-in CLI tool for easy management:

```bash
# Check current status
shibudb manager 9090 status

# View detailed statistics
shibudb manager 9090 stats

# Set specific limit
shibudb manager 9090 limit 2000

# Increase limit by 500
shibudb manager 9090 increase 500

# Decrease limit by 200
shibudb manager 9090 decrease 200

# Check server health
shibudb manager 9090 health
```

### 3. Unix Signals

Send signals to the server process for quick adjustments:

```bash
# Increase limit by 100
kill -USR1 <server_pid>

# Decrease limit by 100
kill -USR2 <server_pid>
```

## Implementation Details

### Connection Manager Architecture

```go
type ConnectionManager struct {
    maxConnections    int32
    activeConnections int32
    semaphore         chan struct{}
    connections       sync.Map
    mu                sync.RWMutex
    limitUpdateChan   chan int32
    shutdownChan      chan struct{}
}
```

### Dynamic Limit Updates

1. **Safety Validation**: Checks that new limit isn't below active connections
2. **Atomic Updates**: Uses mutex to ensure thread-safe updates
3. **Semaphore Resizing**: Dynamically resizes the semaphore channel
4. **Permit Transfer**: Preserves existing connection permits during updates

### Performance Characteristics

- **Minimal Overhead**: Updates are O(1) with negligible impact
- **Lock-free Reads**: Connection acquisition uses RLock for better performance
- **Buffered Channels**: Limit updates are buffered to prevent blocking
- **Graceful Degradation**: Failed updates don't affect existing connections

## Usage Examples

### Production Environment

```bash
# Start server with conservative limit
sudo shibudb start 9090 1000

# Monitor usage
shibudb manager 9090 stats

# Scale up during peak hours
shibudb manager 9090 increase 500

# Scale down during off-peak
shibudb manager 9090 decrease 300
```

### Development Environment

```bash
# Start with low limit for testing
sudo shibudb start 9090 100

# Increase for load testing
shibudb manager 9090 limit 1000

# Reset to original limit
shibudb manager 9090 limit 100
```

### Automated Scaling

```bash
#!/bin/bash
# Auto-scale based on usage

while true; do
    # Get current usage
    usage=$(curl -s http://localhost:10090/stats | jq -r '.usage_percentage')
    
    if (( $(echo "$usage > 80" | bc -l) )); then
        echo "High usage detected: ${usage}%"
        curl -X POST http://localhost:10090/limit/increase \
             -H "Content-Type: application/json" \
             -d '{"amount": 200}'
    elif (( $(echo "$usage < 30" | bc -l) )); then
        echo "Low usage detected: ${usage}%"
        curl -X POST http://localhost:10090/limit/decrease \
             -H "Content-Type: application/json" \
             -d '{"amount": 100}'
    fi
    
    sleep 60
done
```

## Error Handling

### Common Error Scenarios

**Setting Limit Below Active Connections**
```json
{
  "error": "cannot set limit to 500 when 750 connections are active",
  "status": "failed"
}
```

**Invalid Limit Value**
```json
{
  "error": "connection limit must be positive",
  "status": "failed"
}
```

**Management Server Unavailable**
```bash
Error: Failed to connect to management server: connection refused
```

### Troubleshooting

1. **Management Server Not Responding**
   - Check if server is running: `shibudb manager 9090 health`
   - Verify management port: `<main_port> + 1000`
   - Check firewall settings

2. **Limit Updates Failing**
   - Ensure new limit is above active connections
   - Check server logs for detailed error messages
   - Verify you have proper permissions

3. **High Memory Usage**
   - Monitor connection statistics regularly
   - Consider reducing limits during low usage
   - Check for connection leaks

## Monitoring and Alerting

### Key Metrics

- **Active Connections**: Current number of connected clients
- **Connection Usage**: Percentage of limit being used
- **Available Slots**: Remaining connection capacity
- **Limit Changes**: History of limit modifications

### Alerting Examples

```bash
# Alert when usage exceeds 80%
usage=$(shibudb manager 9090 stats | grep "Usage Percentage" | awk '{print $3}' | sed 's/%//')
if (( $(echo "$usage > 80" | bc -l) )); then
    echo "WARNING: High connection usage: ${usage}%"
    # Send alert via email, Slack, etc.
fi

# Alert when limit changes
old_limit=$(shibudb manager 9090 status | grep "Current Limit" | awk '{print $3}')
sleep 60
new_limit=$(shibudb manager 9090 status | grep "Current Limit" | awk '{print $3}')
if [ "$old_limit" != "$new_limit" ]; then
    echo "INFO: Connection limit changed from $old_limit to $new_limit"
fi
```

## Best Practices

### 1. Start Conservative
```bash
# Start with lower limits and scale up
sudo shibudb start 9090 500
```

### 2. Monitor Regularly
```bash
# Set up monitoring
watch -n 30 'shibudb manager 9090 stats'
```

### 3. Use Appropriate Update Methods
- **Signals**: Quick adjustments during emergencies
- **CLI**: Interactive management and scripting
- **HTTP API**: Integration with monitoring systems

### 4. Plan for Scaling
```bash
# Pre-configure scaling scripts
cat > scale_up.sh << 'EOF'
#!/bin/bash
shibudb manager 9090 increase 200
echo "$(date): Increased connection limit"
EOF

cat > scale_down.sh << 'EOF'
#!/bin/bash
shibudb manager 9090 decrease 100
echo "$(date): Decreased connection limit"
EOF
```

## Security Considerations

### Management API Security

- **Network Access**: Management API runs on separate port
- **No Authentication**: Currently no auth on management API (consider firewall rules)
- **Local Access**: Management API only accessible from localhost

### Recommended Security Measures

```bash
# Restrict management API access
iptables -A INPUT -p tcp --dport 10090 -s 127.0.0.1 -j ACCEPT
iptables -A INPUT -p tcp --dport 10090 -j DROP

# Use SSH tunnel for remote management
ssh -L 10090:localhost:10090 user@server
```

## Performance Impact

### Overhead Analysis

- **Connection Acquisition**: ~0.1ms additional latency
- **Limit Updates**: ~1ms for typical updates
- **Memory Usage**: Minimal additional overhead
- **CPU Impact**: Negligible for normal operations

### Benchmarks

```bash
# Test connection acquisition performance
time for i in {1..1000}; do
    shibudb manager 9090 status > /dev/null
done

# Test limit update performance
time shibudb manager 9090 increase 100
```