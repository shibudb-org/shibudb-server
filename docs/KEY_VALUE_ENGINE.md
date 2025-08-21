# ShibuDb Key-Value Engine Guide

## Table of Contents

- [Overview](#overview)
- [Key-Value Space Management](#key-value-space-management)
- [WAL Configuration for Key-Value Spaces](#wal-configuration-for-key-value-spaces)
- [Basic Operations](#basic-operations)
- [Advanced Operations](#advanced-operations)
- [Data Types and Formats](#data-types-and-formats)
- [Performance Considerations](#performance-considerations)
- [Best Practices](#best-practices)
- [Examples and Use Cases](#examples-and-use-cases)
- [Troubleshooting](#troubleshooting)

## Overview

The Key-Value Engine in ShibuDb provides fast, reliable storage for simple key-value pairs. It's built on top of a B-tree index for efficient lookups and includes Write-Ahead Logging (WAL) for data durability.

### Key Features

- **High Performance**: B-tree indexing for fast key lookups
- **Data Durability**: Write-Ahead Logging ensures data persistence
- **Atomic Operations**: Each PUT/GET/DELETE operation is atomic
- **Space Isolation**: Data is organized in separate spaces
- **Role-Based Access**: Fine-grained permissions per space

### Architecture

```
┌─────────────────────────────────────┐
│           Key-Value Space           │
├─────────────────────────────────────┤
│  B-Tree Index (for fast lookups)    │
├─────────────────────────────────────┤
│  In-Memory Buffer (for performance) │
├─────────────────────────────────────┤
│  Write-Ahead Log (for durability)   │
├─────────────────────────────────────┤
│  Data Files (persistent storage)    │
└─────────────────────────────────────┘
```

## Key-Value Space Management

### Creating a Key-Value Space

```bash
# Create a basic key-value space
CREATE-SPACE users --engine key-value

# Create with custom name
CREATE-SPACE product_catalog --engine key-value

# Create with WAL enabled (default, for enhanced durability)
CREATE-SPACE durable_users --engine key-value --enable-wal

# Create with WAL disabled (for maximum performance)
CREATE-SPACE fast_cache --engine key-value --disable-wal
```

**Parameters:**
- `--engine key-value`: Specifies key-value engine type
- `--enable-wal`: Enable Write-Ahead Logging for enhanced durability (default for key-value spaces)
- `--disable-wal`: Disable Write-Ahead Logging for maximum performance

**Note**: Only admin users can create spaces.

### Listing Spaces

```bash
# List all available spaces
LIST-SPACES
```

Response:
```json
{
  "status": "OK",
  "spaces": ["users", "product_catalog", "session_data"]
}
```

### Using a Space

```bash
# Switch to a specific space
USE users

# Verify current space (prompt will show current space)
[users]>
```

### WAL Configuration for Key-Value Spaces

Key-value spaces support configurable Write-Ahead Logging (WAL) to balance performance and durability:

#### Default Behavior (WAL Enabled)
- **Performance**: Slightly slower due to WAL overhead
- **Durability**: Enhanced durability with full crash recovery
- **Recovery**: Complete recovery from crashes and unexpected shutdowns
- **Use Case**: Traditional database workloads requiring strong durability

#### WAL Disabled
- **Performance**: Maximum write performance (~20-30% faster)
- **Durability**: Basic durability through data file persistence
- **Recovery**: Limited recovery capabilities
- **Use Case**: High-performance applications where some data loss is acceptable

#### WAL Configuration Examples

```bash
# Create key-value space with WAL enabled (enhanced durability, default)
CREATE-SPACE production_data --engine key-value --enable-wal

# Create key-value space with WAL disabled (maximum performance)
CREATE-SPACE cache_data --engine key-value --disable-wal

# Create key-value space with WAL enabled (explicit, same as default)
CREATE-SPACE durable_data --engine key-value
```

#### When to Use WAL

**Use WAL Enabled (`--enable-wal` or default) for:**
- Production systems with critical data
- User data, session information, and configuration
- Applications requiring strong durability guarantees
- Systems where data loss is unacceptable
- Traditional database workloads

**Use WAL Disabled (`--disable-wal`) for:**
- High-performance caching layers
- Temporary or session data that can be regenerated
- Development and testing environments
- Applications where some data loss is acceptable
- Real-time processing with strict latency requirements

### Deleting a Space

```bash
# Delete a space (admin only)
DELETE-SPACE users
```

**Warning**: This permanently removes all data in the space.

## Basic Operations

### PUT - Store Data

```bash
# Store a simple value
PUT user:1 "John Doe"

# Store with complex key
PUT user:profile:123 "{\"name\":\"John\",\"age\":30}"

# Store with special characters in key
PUT "user:email:john@example.com" "verified"
```

### GET - Retrieve Data

```bash
# Get a value by key
GET user:1

# Get with complex key
GET user:profile:123

# Get with special characters
GET "user:email:john@example.com"
```

### DELETE - Remove Data

```bash
# Delete a key-value pair
DELETE user:1

# Delete with complex key
DELETE user:profile:123
```

## Advanced Operations

### Key Patterns and Organization

#### Hierarchical Keys

```bash
# User data organization
PUT users:123:profile:name "John Doe"
PUT users:123:profile:email "john@example.com"
PUT users:123:profile:phone "+1-555-0123"

# Session data
PUT sessions:abc123:user_id "123"
PUT sessions:abc123:created "2024-01-15T10:30:00Z"
PUT sessions:abc123:last_activity "2024-01-15T11:45:00Z"

# Product catalog
PUT products:electronics:laptops:macbook:sku "MBP-001"
PUT products:electronics:laptops:macbook:price "1299.99"
PUT products:electronics:laptops:macbook:stock "50"
```

#### Namespaced Keys

```bash
# Application-specific namespaces
PUT app:config:database:host "localhost"
PUT app:config:database:port "5432"
PUT app:config:redis:host "127.0.0.1"

# Feature flags
PUT flags:new_ui:enabled "true"
PUT flags:beta_features:enabled "false"
```

## Data Types and Formats

### Supported Data Types

ShibuDb stores all data as strings, but you can store various data types:

#### Text Data

```bash
# Simple text
PUT message:1 "Hello, World!"

# JSON data
PUT user:1 "{\"name\":\"John\",\"age\":30,\"city\":\"New York\"}"

# XML data
PUT config:1 "<config><database><host>localhost</host></database></config>"
```

#### Numeric Data

```bash
# Store numbers as strings
PUT counter:visits "12345"
PUT price:product:1 "29.99"
PUT score:user:123 "95.5"
```

#### Binary Data (Base64)

```bash
# Store binary data as base64
PUT image:avatar:123 "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
```

### Data Serialization

#### JSON Storage

```bash
# Store complex objects as JSON
PUT user:profile:123 '{"name":"John Doe","email":"john@example.com","preferences":{"theme":"dark","notifications":true}}'

# Store arrays
PUT user:friends:123 '["user:456","user:789","user:101"]'

# Store nested objects
PUT product:details:456 '{"name":"Laptop","specs":{"cpu":"Intel i7","ram":"16GB","storage":"512GB SSD"}}'
```

#### Custom Formats

```bash
# CSV-like format
PUT data:users:123 "John Doe,john@example.com,30,New York"

# Key-value pairs
PUT config:app "host=localhost;port=8080;debug=true"

# Delimited lists
PUT tags:post:789 "tech,programming,golang,database"
```

## Performance Considerations

### Key Design

#### Optimal Key Length

```bash
# Good: Short, descriptive keys
PUT u:1 "John Doe"
PUT u:1:e "john@example.com"

# Avoid: Very long keys
PUT very_long_key_name_that_is_unnecessarily_long_and_hard_to_read "value"
```

### Memory Usage

#### Data Size Considerations

```bash
# Small values (efficient)
PUT flag:feature "true"
PUT counter:visits "12345"

# Large values (use sparingly)
PUT data:large_file "very_long_content_here..."
```

## Best Practices

### 1. Key Naming Conventions

#### Use Consistent Separators

```bash
# Good: Use colons for hierarchy
PUT users:123:profile:name "John Doe"
PUT users:123:profile:email "john@example.com"

# Good: Use underscores for flat structures
PUT user_profiles:123 "John Doe"
PUT user_emails:123 "john@example.com"
```

#### Avoid Special Characters

```bash
# Good: Alphanumeric and basic punctuation
PUT user:123:name "John Doe"
PUT config:app:debug "true"

# Avoid: Special characters that might cause issues
PUT user/123/name "John Doe"
PUT config@app#debug "true"
```

### 2. Data Organization

#### Logical Grouping

```bash
# Group related data
PUT users:123:profile:name "John Doe"
PUT users:123:profile:email "john@example.com"
PUT users:123:profile:phone "+1-555-0123"

# Separate different types of data
PUT sessions:abc123:user_id "123"
PUT sessions:abc123:created "2024-01-15T10:30:00Z"
```

#### Versioning Strategy

```bash
# Version your data
PUT users:123:v1:profile "old_profile_data"
PUT users:123:v2:profile "new_profile_data"

# Or use timestamps
PUT users:123:2024-01-15:profile "profile_data"
PUT users:123:2024-01-16:profile "updated_profile_data"
```

### 3. Error Handling

#### Handle Large Data

```bash
# For large values, consider chunking
PUT data:large:1:chunk1 "first_part_of_data"
PUT data:large:1:chunk2 "second_part_of_data"
PUT data:large:1:chunk3 "third_part_of_data"
```

## Examples and Use Cases

### 1. User Management System

```bash
# Create user space (WAL enabled by default for durability)
CREATE-SPACE user_management --engine key-value
USE user_management

# Alternative: Create with explicit WAL configuration
CREATE-SPACE user_management_durable --engine key-value --enable-wal

# Store user profiles
PUT users:123:profile "{\"name\":\"John Doe\",\"email\":\"john@example.com\",\"age\":30}"
PUT users:456:profile "{\"name\":\"Jane Smith\",\"email\":\"jane@example.com\",\"age\":25}"

# Store user sessions
PUT sessions:abc123:user_id "123"
PUT sessions:abc123:created "2024-01-15T10:30:00Z"
PUT sessions:abc123:last_activity "2024-01-15T11:45:00Z"

# Store user preferences
PUT preferences:123:theme "dark"
PUT preferences:123:language "en"
PUT preferences:123:notifications "true"
```

### 2. Configuration Management

```bash
# Create config space
CREATE-SPACE app_config --engine key-value
USE app_config

# Application configuration
PUT config:app:name "MyApp"
PUT config:app:version "1.0.0"
PUT config:app:debug "false"

# Database configuration
PUT config:database:host "localhost"
PUT config:database:port "5432"
PUT config:database:name "myapp_db"
PUT config:database:user "app_user"

# Feature flags
PUT flags:new_ui:enabled "true"
PUT flags:beta_features:enabled "false"
PUT flags:maintenance_mode:enabled "false"
```

### 3. Caching Layer

```bash
# Create cache space (WAL disabled for maximum performance)
CREATE-SPACE cache --engine key-value --disable-wal
USE cache

# Alternative: Create with WAL enabled if cache durability is important
CREATE-SPACE persistent_cache --engine key-value --enable-wal

# Cache API responses
PUT cache:api:users:123 "{\"id\":123,\"name\":\"John Doe\",\"email\":\"john@example.com\"}"
PUT cache:api:products:456 "{\"id\":456,\"name\":\"Laptop\",\"price\":999.99}"

# Cache with expiration (store timestamp)
PUT cache:expiry:api:users:123 "2024-01-15T12:00:00Z"
PUT cache:expiry:api:products:456 "2024-01-15T12:30:00Z"
```

### 4. Session Management

```bash
# Create session space
CREATE-SPACE sessions --engine key-value
USE sessions

# Store session data
PUT session:abc123:user_id "123"
PUT session:abc123:created "2024-01-15T10:30:00Z"
PUT session:abc123:last_activity "2024-01-15T11:45:00Z"
PUT session:abc123:ip_address "192.168.1.100"
PUT session:abc123:user_agent "Mozilla/5.0..."

# Store session permissions
PUT session:abc123:permissions "read,write,admin"
```

### 5. Analytics and Metrics

```bash
# Create metrics space
CREATE-SPACE metrics --engine key-value
USE metrics

# Page view counters
PUT metrics:page_views:homepage "12345"
PUT metrics:page_views:products "6789"
PUT metrics:page_views:about "1234"

# User activity
PUT metrics:active_users:today "156"
PUT metrics:active_users:yesterday "142"
PUT metrics:active_users:this_week "892"

# Performance metrics
PUT metrics:response_time:api:users "45ms"
PUT metrics:response_time:api:products "32ms"
PUT metrics:error_rate:api:users "0.1%"
```

## Troubleshooting

### Common Issues

#### 1. "No space selected" Error

**Problem**: Trying to perform operations without selecting a space

**Solution**:
```bash
# Select a space first
USE my_space

# Then perform operations
PUT key "value"
GET key
```

#### 2. "Space does not exist" Error

**Problem**: Trying to use a non-existent space

**Solution**:
```bash
# List available spaces
LIST-SPACES

# Create the space if needed (admin only)
CREATE-SPACE my_space --engine key-value

# Then use it
USE my_space
```

#### 3. "Write permission denied" Error

**Problem**: User doesn't have write permissions for the space

**Solution**:
```bash
# Check user permissions (admin only)
GET-USER current_user

# Update user permissions (admin only)
UPDATE-USER-PERMISSIONS username
# Add permission: my_space=write
```

#### 4. Performance Issues

**Problem**: Slow operations or high memory usage

**Solutions**:
```bash
# Check connection statistics
shibudb manager 9090 stats

# Monitor server logs
tail -f /usr/local/var/log/shibudb.log

# Consider key design optimization
# Use shorter keys and better organization
```

### Performance Monitoring

#### Check Space Usage

```bash
# Monitor disk usage
du -sh /usr/local/var/lib/shibudb/

# Check specific space files
ls -la /usr/local/var/lib/shibudb/
```

#### Monitor Operations

```bash
# Watch server logs for performance issues
tail -f /usr/local/var/log/shibudb.log | grep -E "(slow|performance|timeout)"

# Check connection usage
shibudb manager 9090 stats
```

### Data Recovery

#### From WAL (Write-Ahead Log)

If the server crashes, data is automatically recovered from the WAL on restart (if WAL is enabled):

```bash
# Restart server (recovery happens automatically)
sudo shibudb stop
sudo shibudb start 9090

# Check logs for recovery messages
tail -f /usr/local/var/log/shibudb.log
```

**Note**: WAL recovery is only available if the space was created with `--enable-wal` (default for key-value spaces). Spaces created with `--disable-wal` have limited recovery capabilities.

#### Manual Data Export

```bash
# Data is stored in files under /usr/local/var/lib/shibudb/
# Each space has its own directory with data files
ls -la /usr/local/var/lib/shibudb/
```

## Next Steps

After mastering the Key-Value Engine, explore:

- [Vector Engine Guide](VECTOR_ENGINE.md) - Learn vector search capabilities
- [User Management Guide](USER_MANAGEMENT.md) - Set up authentication and permissions
- [Administration Guide](ADMINISTRATION.md) - Server management and monitoring
- [API Reference](API_REFERENCE.md) - Complete command reference 