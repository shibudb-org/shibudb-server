# ShibuDb Setup Guide

## Table of Contents

- [Prerequisites](#prerequisites)
- [Installation Methods](#installation-methods)
  - [From Pre-built Packages](#from-pre-built-packages)
  - [From brew](#from-brew)
- [First Steps](#first-steps)
- [Verification](#verification)
- [Troubleshooting](#troubleshooting)

## Prerequisites

### System Requirements

- **Operating System**: Linux (AMD64/ARM64) or macOS (Apple Silicon)
- **Go Version**: 1.23.0 or later (for source builds)
- **Memory**: Minimum 512MB RAM, recommended 2GB+
- **Disk Space**: Minimum 1GB free space
- **Network**: TCP port access for client connections

## Installation Methods

### From Pre-built Packages

#### macOS (Apple Silicon)

1. Download the `.pkg` file for your architecture
2. Install using the installer:

```bash
sudo installer -pkg shibudb-{version}-apple_silicon.pkg -target /
```

#### Linux (Debian/Ubuntu)

**AMD64:**
```bash
# Download and install
wget https://github.com/Podcopic-Labs/ShibuDb/releases/download/v{version}/shibudb_{version}_amd64.deb
sudo dpkg -i shibudb_{version}_amd64.deb
```

**ARM64:**
```bash
# Download and install
wget https://github.com/Podcopic-Labs/ShibuDb/releases/download/v{version}/shibudb_{version}_arm64.deb
sudo dpkg -i shibudb_{version}_arm64.deb
```

#### Linux (RHEL/CentOS/Fedora)

**AMD64:**
```bash
# Download and install
wget https://github.com/Podcopic-Labs/ShibuDb/releases/download/v{version}/shibudb-{version}-1.x86_64.rpm
sudo rpm -i shibudb-{version}-1.x86_64.rpm
```

**ARM64:**
```bash
# Download and install
wget https://github.com/Podcopic-Labs/ShibuDb/releases/download/v{version}/shibudb-{version}-1.aarch64.rpm
sudo rpm -i shibudb-{version}-1.aarch64.rpm
```

### From brew

If you prefer using Homebrew on macOS, you can install ShibuDb directly from our tap:

```bash
brew tap Podcopic-Labs/shibudb

# Install ShibuDb
brew install shibudb

# If you already have an older version installed, you can upgrade
brew link shibudb


## Initial Configuration

### 1. Create Required Directories
```

```bash
ShibuDb automatically creates the following directory structure:
/usr/local/var/lib/shibudb/     # Database files
/usr/local/var/log/shibudb.log  # Log file
/usr/local/var/run/shibudb.pid  # PID file
```

### 1. Configure Connection Limits

By default, ShibuDb allows up to 1000 concurrent connections. You can modify this:

```bash
# Start with custom connection limit
sudo shibudb start 9090 2000

# Or update at runtime
shibudb manager 9090 limit 2000
```

## First Steps

### 1. Start the Server

```bash
# Start with default settings (port 9090, 1000 connections)
sudo shibudb start 9090

# Start with custom connection limit
# if server is started for first time, it will ask for new admin credentials
sudo shibudb start 9090 500
```

### 2. Connect to the Database

```bash
# Connect to the server
shibudb connect 9090
```

You'll be prompted for credentials:
```
Username: {admin username}
Password: {admin password}
```

### 3. Create Your First Space

```bash
# Create a key-value space
CREATE-SPACE my_data --engine key-value

# Create a vector space for similarity search
CREATE-SPACE my_vectors --engine vector --dimension 128 --index-type Flat --metric L2
```

### 4. Basic Operations

```bash
# Use the space
USE my_data

# Store and retrieve data
PUT user:1 "John Doe"
GET user:1
DELETE user:1

# Vector operations (in vector space)
USE my_vectors
INSERT-VECTOR 1 1.0,2.0,3.0,4.0
SEARCH-TOPK 1.1,2.1,3.1,4.1 5
RANGE-SEARCH 1.0,2.0,3.0,4.0 0.5
```

## Verification

### 1. Check Server Status

```bash
# Check if server is running
ps aux | grep shibudb

# Check server logs
tail -f /usr/local/var/log/shibudb.log
```

### 2. Test Connection

```bash
# Test basic connectivity
telnet localhost 9090

# Test management API
curl http://localhost:10090/health
```

### 3. Verify Management API

```bash
# Get connection statistics
curl http://localhost:10090/stats

# Get current connection limit
curl http://localhost:10090/limit
```

### 4. Run Basic Tests

```bash
# Run unit tests
make test

# Run E2E tests (requires server running on port 4444 with admin credentials as admin:admin)
make e2e-test
```

## Troubleshooting

### Common Issues

#### 1. Permission Denied

**Problem**: `sudo shibudb start` fails with permission errors

**Solution**:
```bash
# Ensure proper ownership
sudo chown -R root:root /usr/local/bin/shibudb
sudo chmod +x /usr/local/bin/shibudb

# Create required directories with proper permissions if not created
sudo mkdir -p /usr/local/var/lib/shibudb
sudo mkdir -p /usr/local/var/log
sudo mkdir -p /usr/local/var/run
```

#### 2. Port Already in Use

**Problem**: Server fails to start because port is occupied

**Solution**:
```bash
# Check what's using the port
sudo lsof -i :9090

# Kill the process or use a different port
sudo shibudb start 9091
```

#### 3. FAISS Library Issues

**Problem**: Vector operations fail with library errors

**Solution**:
```bash
# Check if FAISS libraries are accessible
ldd /usr/local/bin/shibudb | grep faiss

# Set library path if needed
export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH
```

#### 4. Connection Refused

**Problem**: Client can't connect to server

**Solution**:
```bash
# Check if server is running
sudo shibudb stop
sudo shibudb start 9090

# Check firewall settings
sudo ufw allow 9090
sudo ufw allow 10090
```

#### 5. Authentication Failures

**Problem**: Can't login with admin credentials

**Solution**:
```bash
# Reset admin password by recreating users file
sudo rm /usr/local/var/lib/shibudb/users.json
sudo shibudb start 9090
# Default credentials will be recreated: admin/admin
```

### Log Analysis

#### Check Server Logs

```bash
# View recent logs
tail -n 100 /usr/local/var/log/shibudb.log

# Search for errors
grep -i error /usr/local/var/log/shibudb.log

# Monitor logs in real-time
tail -f /usr/local/var/log/shibudb.log
```

#### Common Log Messages

- `"ShibuDB server started on port X"` - Server started successfully
- `"Connection limit reached"` - Too many concurrent connections
- `"authentication failed"` - Invalid credentials
- `"space does not exist"` - Trying to use non-existent space

### Performance Tuning

#### 1. Connection Limits

```bash
# Increase connection limit for high-traffic scenarios
sudo shibudb start 9090 5000

# Monitor connection usage
shibudb manager 9090 stats
```

#### 2. Memory Usage

- Monitor memory usage: `htop` or `top`
- Vector spaces use more memory than key-value spaces
- Consider system resources when choosing index types

#### 3. Disk Space

```bash
# Check database size
du -sh /usr/local/var/lib/shibudb/

# Monitor disk usage
df -h
```

## Next Steps

After successful setup, explore these guides:

- [Key-Value Engine Guide](KEY_VALUE_ENGINE.md) - Learn key-value operations
- [Vector Engine Guide](VECTOR_ENGINE.md) - Master vector search capabilities
- [User Management Guide](USER_MANAGEMENT.md) - Set up authentication and permissions
- [Administration Guide](ADMINISTRATION.md) - Server management and monitoring

## Support

If you encounter issues not covered in this guide:

1. Check the [Troubleshooting Guide](TROUBLESHOOTING.md)
2. Review [GitHub Issues](https://github.com/Podcopic-Labs/ShibuDb/issues)
3. Join [GitHub Discussions](https://github.com/Podcopic-Labs/ShibuDb/discussions)
4. Check the [Architecture Documentation](ARCHITECTURE.md) for technical details 