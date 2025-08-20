# WAL Configuration for Vector Storage

## Overview

ShibuDb now supports configurable Write-Ahead Logging (WAL) for vector storage spaces. By default, WAL is disabled for better performance, but users can enable it for enhanced durability guarantees.

## WAL Behavior

### When WAL is Disabled (Default)
- **Performance**: Faster write operations
- **Durability**: Basic durability through data file persistence
- **Recovery**: Limited recovery capabilities
- **Use Case**: High-performance applications where some data loss is acceptable

### When WAL is Enabled
- **Performance**: Slightly slower due to additional logging
- **Durability**: Enhanced durability with crash recovery
- **Recovery**: Full recovery from crashes and unexpected shutdowns
- **Use Case**: Applications requiring strong durability guarantees

## Usage

### Command Line Interface

Create a vector space with WAL disabled (default):
```bash
create-space my_vector_space --engine vector --dimension 128 --index-type Flat --metric L2
```

Create a vector space with WAL enabled:
```bash
create-space my_vector_space --engine vector --dimension 128 --index-type Flat --metric L2 --enable-wal
```

### Programmatic API

```go
// Create space with WAL disabled (default)
space, err := spaceManager.CreateSpace("my_space", "vector", 128, "Flat", "L2")

// Create space with WAL enabled
space, err := spaceManager.CreateSpaceWithWAL("my_space", "vector", 128, "Flat", "L2", true)
```

## Configuration Details

- **WAL Files**: When enabled, WAL files are stored as `vector_wal.db` in the space directory
- **Backward Compatibility**: Existing spaces without WAL configuration default to WAL disabled
- **Space Metadata**: WAL setting is stored in space metadata and persists across restarts

## Performance Considerations

- **WAL Disabled**: ~20-30% faster write operations
- **WAL Enabled**: Additional I/O operations for logging, but provides crash recovery
- **Memory Usage**: WAL enabled uses slightly more memory for buffering

## Migration

Existing vector spaces will continue to work without changes. To enable WAL for an existing space, you would need to:
1. Delete the existing space
2. Recreate it with WAL enabled
3. Re-insert your data

## Recommendations

- **Use WAL Disabled** for: Development, testing, high-throughput applications where some data loss is acceptable
- **Use WAL Enabled** for: Production systems, critical data, applications requiring strong durability guarantees
