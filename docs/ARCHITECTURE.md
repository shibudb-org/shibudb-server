# ShibuDb Architecture

## Overview

ShibuDb is a lightweight, embedded database system designed for high-performance storage and retrieval with vector search capabilities. The architecture follows a modular design pattern with clear separation of concerns.

## System Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Client Applications                     │
└─────────────────────┬───────────────────────────────────────┘
                      │ TCP/JSON
┌─────────────────────▼───────────────────────────────────────┐
│                    Network Layer                           │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────┐ │
│  │   TCP Server    │  │  Connection     │  │   Protocol  │ │
│  │                 │  │   Manager       │  │   Handler    │ │
│  └─────────────────┘  └─────────────────┘  └─────────────┘ │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                    Query Engine                            │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────┐ │
│  │   Query Parser  │  │  Query Router   │  │  Response   │ │
│  │                 │  │                 │  │  Builder    │ │
│  └─────────────────┘  └─────────────────┘  └─────────────┘ │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                    Authentication                          │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────┐ │
│  │   Auth Manager  │  │  Role Manager   │  │  Permission │ │
│  │                 │  │                 │  │  Checker    │ │
│  └─────────────────┘  └─────────────────┘  └─────────────┘ │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                    Storage Layer                           │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────┐ │
│  │  Space Manager  │  │  Key-Value      │  │  Vector     │ │
│  │                 │  │  Storage        │  │  Storage    │ │
│  └─────────────────┘  └─────────────────┘  └─────────────┘ │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                    Index Layer                             │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────┐ │
│  │   B-Tree Index  │  │  Vector Index   │  │  Metadata   │ │
│  │                 │  │  (FAISS)        │  │  Index      │ │
│  └─────────────────┘  └─────────────────┘  └─────────────┘ │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                    Persistence Layer                       │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────┐ │
│  │   WAL (Write-   │  │  Data Files     │  │  Checkpoint │ │
│  │   Ahead Log)    │  │                 │  │  Manager    │ │
│  └─────────────────┘  └─────────────────┘  └─────────────┘ │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────┐
│                    File System                             │
└─────────────────────────────────────────────────────────────┘
```

## Core Components

### 1. Network Layer

The network layer handles all client communication via TCP connections.

**Key Components:**
- **TCP Server**: Listens for incoming connections
- **Connection Manager**: Manages connection lifecycle and pooling
- **Protocol Handler**: Parses JSON messages and routes to query engine

**Responsibilities:**
- Accept and manage TCP connections
- Parse JSON messages
- Route requests to appropriate handlers
- Send responses back to clients
- Handle connection timeouts and cleanup

### 2. Query Engine

The query engine is the central coordinator for all database operations.

**Key Components:**
- **Query Parser**: Validates and parses incoming queries
- **Query Router**: Routes queries to appropriate handlers
- **Response Builder**: Constructs standardized responses

**Responsibilities:**
- Validate query syntax and semantics
- Route queries to appropriate storage engines
- Coordinate multi-step operations
- Build consistent response formats
- Handle query timeouts and cancellation

### 3. Authentication System

The authentication system provides role-based access control.

**Key Components:**
- **Auth Manager**: Handles user authentication
- **Role Manager**: Manages user roles and permissions
- **Permission Checker**: Validates operation permissions

**Responsibilities:**
- Authenticate users via username/password
- Manage user roles (admin, user, read-only)
- Enforce space-level permissions
- Track user sessions
- Provide audit logging

### 4. Storage Layer

The storage layer manages data persistence and retrieval.

**Key Components:**
- **Space Manager**: Manages data spaces (namespaces)
- **Key-Value Storage**: Handles traditional key-value operations
- **Vector Storage**: Manages vector data and similarity search

**Responsibilities:**
- Organize data into logical spaces
- Store and retrieve key-value pairs
- Manage vector data and indexes
- Handle data compression and optimization
- Provide transaction support

### 5. Index Layer

The index layer provides fast data access patterns.

**Key Components:**
- **B-Tree Index**: Provides fast key-value lookups
- **Vector Index (FAISS)**: Enables similarity search
- **Metadata Index**: Tracks system metadata

**Responsibilities:**
- Maintain indexes for fast data access
- Support range queries and scans
- Enable vector similarity search
- Optimize index performance
- Handle index maintenance and rebuilding

### 6. Persistence Layer

The persistence layer ensures data durability and recovery.

**Key Components:**
- **WAL (Write-Ahead Log)**: Ensures ACID compliance
- **Data Files**: Store actual data on disk
- **Checkpoint Manager**: Creates recovery points

**Responsibilities:**
- Ensure data durability
- Provide crash recovery
- Manage data file organization
- Handle backup and restore
- Optimize I/O performance

## Data Flow

### 1. Write Operation Flow

```
Client Request → Network Layer → Query Engine → Auth Check → Storage Layer → Index Update → WAL Write → Response
```

1. **Client Request**: JSON query arrives via TCP
2. **Network Layer**: Parses JSON and validates format
3. **Query Engine**: Validates query semantics and routes
4. **Auth Check**: Verifies user permissions
5. **Storage Layer**: Performs the actual write operation
6. **Index Update**: Updates relevant indexes
7. **WAL Write**: Logs operation for durability
8. **Response**: Returns success/error to client

### 2. Read Operation Flow

```
Client Request → Network Layer → Query Engine → Auth Check → Index Lookup → Storage Layer → Response
```

1. **Client Request**: JSON query arrives via TCP
2. **Network Layer**: Parses JSON and validates format
3. **Query Engine**: Validates query semantics and routes
4. **Auth Check**: Verifies user permissions
5. **Index Lookup**: Uses indexes for fast access
6. **Storage Layer**: Retrieves data from storage
7. **Response**: Returns data to client

### 3. Vector Search Flow

```
Client Request → Network Layer → Query Engine → Auth Check → Vector Index → Similarity Search → Response
```

1. **Client Request**: Vector search query arrives
2. **Network Layer**: Parses JSON and validates format
3. **Query Engine**: Validates query semantics
4. **Auth Check**: Verifies user permissions
5. **Vector Index**: Uses FAISS for similarity search
6. **Similarity Search**: Finds similar vectors
7. **Response**: Returns ranked results

## Data Organization

### Spaces

ShibuDb organizes data into logical spaces (similar to databases in traditional systems):

```
Space: "users"
├── Key: "user:123:profile" → Value: {"name": "John", "email": "john@example.com"}
├── Key: "user:123:settings" → Value: {"theme": "dark", "notifications": true}
└── Key: "user:456:profile" → Value: {"name": "Jane", "email": "jane@example.com"}

Space: "products"
├── Key: "product:789:info" → Value: {"name": "Laptop", "price": 999.99}
└── Vector: "product:789:embedding" → Vector: [0.1, 0.2, 0.3, ...]
```

### Indexes

**B-Tree Index**: Provides fast key-value lookups
```
Key: "user:123:profile" → File Offset: 1024
Key: "user:123:settings" → File Offset: 2048
Key: "user:456:profile" → File Offset: 3072
```

**Vector Index (FAISS)**: Enables similarity search
```
Space: "products"
├── Index Type: IVF (Inverted File)
├── Vector Dimension: 128
├── Number of Clusters: 100
└── Distance Metric: L2
```

## Storage Format

### Data Files

```
shibudb_data.db
├── Header (Magic Number, Version, Metadata)
├── Data Blocks
│   ├── Block 1: Key-Value Pairs
│   ├── Block 2: Key-Value Pairs
│   └── ...
└── Footer (Checksum, Size)
```

### WAL (Write-Ahead Log)

```
shibudb_wal.db
├── Log Entry 1: {"op": "PUT", "space": "users", "key": "user:123", "value": "..."}
├── Log Entry 2: {"op": "DELETE", "space": "users", "key": "user:456"}
└── ...
```

### Index Files

```
index.dat
├── B-Tree Index
│   ├── Root Node
│   ├── Internal Nodes
│   └── Leaf Nodes
└── Vector Index
    ├── FAISS Index
    └── Metadata
```

## Performance Characteristics

### Throughput

- **Key-Value Operations**: 10,000-50,000 ops/sec (depending on key size)
- **Vector Operations**: 1,000-5,000 ops/sec (depending on vector dimension)
- **Vector Search**: 100-1,000 queries/sec (depending on index size)

### Latency

- **Key-Value Read**: < 1ms (with B-tree index)
- **Key-Value Write**: < 5ms (including WAL write)
- **Vector Search**: 10-100ms (depending on result size)

### Memory Usage

- **Index Memory**: ~100MB per 1M keys
- **Vector Index**: ~1GB per 1M vectors (128-dimensional)
- **Connection Memory**: ~1MB per connection

## Scalability Considerations

### Horizontal Scaling

ShibuDb is designed for single-instance deployment. For horizontal scaling:

1. **Application-Level Sharding**: Route requests to different ShibuDb instances
2. **Space-Based Partitioning**: Assign different spaces to different instances
3. **Load Balancing**: Use load balancer to distribute requests

### Vertical Scaling

1. **Memory**: Increase available RAM for larger indexes
2. **CPU**: More cores for concurrent operations
3. **Storage**: Faster SSDs for better I/O performance
4. **Network**: Higher bandwidth for more connections

## Security Model

### Authentication

- **Username/Password**: Simple authentication mechanism
- **Password Hashing**: bcrypt for secure password storage
- **Session Management**: Per-connection authentication

### Authorization

- **Role-Based Access Control**: Admin, User, Read-Only roles
- **Space-Level Permissions**: Control access to specific spaces
- **Operation-Level Permissions**: Control specific operations

### Data Protection

- **No Encryption**: Data is stored in plain text
- **Network Security**: Rely on network-level security (TLS, VPN)
- **File Permissions**: Use OS-level file permissions

## Monitoring and Observability

### Metrics

- **Connection Count**: Number of active connections
- **Query Rate**: Queries per second
- **Response Time**: Average response time
- **Error Rate**: Percentage of failed queries
- **Memory Usage**: Current memory consumption
- **Disk Usage**: Storage space utilization

### Logging

- **Access Logs**: All client connections and queries
- **Error Logs**: Failed operations and exceptions
- **Performance Logs**: Slow queries and bottlenecks
- **Security Logs**: Authentication and authorization events

## Backup and Recovery

### Backup Strategy

1. **File-Level Backup**: Copy data files to backup location
2. **WAL Archiving**: Archive write-ahead logs
3. **Index Backup**: Backup index files separately
4. **Configuration Backup**: Backup configuration files

### Recovery Process

1. **Stop Database**: Gracefully shut down the database
2. **Restore Files**: Copy backup files to data directory
3. **Replay WAL**: Apply any pending write-ahead logs
4. **Verify Indexes**: Rebuild indexes if necessary
5. **Start Database**: Restart the database service