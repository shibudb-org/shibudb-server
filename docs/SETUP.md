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
wget https://github.com/shibudb.org/shibudb-server/releases/download/v{version}/shibudb_{version}_amd64.deb
sudo dpkg -i shibudb_{version}_amd64.deb
```

**ARM64:**
```bash
# Download and install
wget https://github.com/shibudb.org/shibudb-server/releases/download/v{version}/shibudb_{version}_arm64.deb
sudo dpkg -i shibudb_{version}_arm64.deb
```

#### Linux (RHEL/CentOS/Fedora)

**AMD64:**
```bash
# Download and install
wget https://github.com/shibudb.org/shibudb-server/releases/download/v{version}/shibudb-{version}-1.x86_64.rpm
sudo rpm -i shibudb-{version}-1.x86_64.rpm
```

**ARM64:**
```bash
# Download and install
wget https://github.com/shibudb.org/shibudb-server/releases/download/v{version}/shibudb-{version}-1.aarch64.rpm
sudo rpm -i shibudb-{version}-1.aarch64.rpm
```

### From brew

If you prefer using Homebrew on macOS, you can install ShibuDb directly from our tap:

```bash
brew tap shibudb.org/shibudb

# Install ShibuDb
brew install shibudb

# If you already have an older version installed, you can upgrade
brew link shibudb


## Initial Configuration

### 1. Create Required Directories
```

```bash
ShibuDb automatically creates the following directory structure:
~/.shibudb/lib/             # Database + config files
~/.shibudb/log/shibudb.log  # Log file
~/.shibudb/run/shibudb.pid  # PID file
```

### 1. Configure Connection Limits

By default, ShibuDb allows up to 1000 concurrent connections. You can modify this:

```bash
# Start with custom connection limit (default listen port is 4444)
shibudb start --max-connections 2000

# Or set default via environment for that shell
SHIBUDB_MAX_CONNECTIONS=2000 shibudb start

# Custom port and limit
shibudb start --max-connections 2000 --port 9090

# Or update at runtime (default management port is 5444 when using default main port 4444)
shibudb manager limit 2000
```

## First Steps

### 1. Start the Server

```bash
# Start with defaults (listen port 4444, 1000 connections); first start will prompt for admin credentials
shibudb start

# First start with admin credentials (non-interactive)
shibudb start --admin-user admin --admin-password admin

# Custom listen port (e.g. 9090)
shibudb start --port 9090

# Custom connection limit
sudo shibudb start --max-connections 500 --port 9090
```

### 2. Connect to the Database

```bash
# Connect to the server (default main port 4444; use --port when different)
shibudb connect
You'll be prompted for credentials:
```
Username: {admin username}
Password: {admin password}
```

```bash
# Connect to the server with credentials
shibudb connect --port 9090 --username admin --password admin
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
tail -f ~/.shibudb/log/shibudb.log
```

### 2. Test Connection

```bash
# Test basic connectivity (default listen port 4444)
telnet localhost 4444

# Test management API (default management port 5444, set with start/run --management-port)
curl http://localhost:5444/health
```

### 3. Verify Management API

```bash
# Get connection statistics
curl http://localhost:5444/stats

# Get current connection limit
curl http://localhost:5444/limit
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
mkdir -p ~/.shibudb/lib ~/.shibudb/log ~/.shibudb/run
```

#### 2. Port Already in Use

**Problem**: Server fails to start because port is occupied

**Solution**:
```bash
# Check what's using the port
sudo lsof -i :4444

# Kill the process or use a different port
sudo shibudb start --port 9091
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
sudo shibudb start

# Check firewall settings (default listen 4444, management 5444)
sudo ufw allow 4444
sudo ufw allow 5444
```

#### 5. Authentication Failures

**Problem**: Can't login with admin credentials

**Solution**:
```bash
# Reset admin password by recreating users file
rm ~/.shibudb/lib/users.json
sudo shibudb start
# Default credentials will be recreated: admin/admin
```

### Log Analysis

#### Check Server Logs

```bash
# View recent logs
tail -n 100 ~/.shibudb/log/shibudb.log

# Search for errors
grep -i error ~/.shibudb/log/shibudb.log

# Monitor logs in real-time
tail -f ~/.shibudb/log/shibudb.log
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
sudo shibudb start --max-connections 5000

# Monitor connection usage (use your listen port)
shibudb manager stats
```

#### 2. Memory Usage

- Monitor memory usage: `htop` or `top`
- Vector spaces use more memory than key-value spaces
- Consider system resources when choosing index types

#### 3. Disk Space

```bash
# Check database size
du -sh ~/.shibudb/lib/

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
2. Review [GitHub Issues](https://github.com/shibudb.org/shibudb-server/issues)
3. Join [GitHub Discussions](https://github.com/shibudb.org/shibudb-server/discussions)
4. Check the [Architecture Documentation](ARCHITECTURE.md) for technical details 