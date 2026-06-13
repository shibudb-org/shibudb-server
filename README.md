# ShibuDb

[![Go Version](https://img.shields.io/badge/Go-1.23.0-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-AGPL%203.0-green.svg)](LICENSE)
[![Platforms](https://img.shields.io/badge/Platforms-Linux%20%7C%20macOS-blue.svg)](https://github.com/shibudb-org/shibudb-server)

ShibuDb is a lightweight database system with vector search capabilities powered by FAISS. It provides high-performance storage and retrieval with support for both traditional key-value operations and advanced vector similarity search.

## ✨ Key Features

- **🔍 Vector Search**: Advanced similarity search using FAISS
- **🏷️ Metadata Filtering**: Pre-filter vector search by indexed metadata fields on Flat spaces (`--where` filters)
- **🗄️ Multi-Space Architecture**: Organize data into separate spaces
- **🔐 Role-Based Access Control**: Secure authentication and authorization
- **⚡ High Performance**: Optimized storage with B-tree indexing
- **🌐 Cross-Platform**: Linux (AMD64/ARM64) and macOS (AMD64/ARM64)
- **📊 Dynamic Connection Management**: Runtime connection limit updates
- **🛡️ Data Durability**: Write-Ahead Logging for crash recovery

## 🚀 Quick Start

### Installation

```bash
# From source
git clone https://github.com/shibudb-org/shibudb-server.git
cd shibudb-server

# Start the local server on port 4444.
# This dev script bootstraps an admin user as admin:admin (see scripts/start-local-server.sh).
make start-local-server
```

### Connect and Use

```bash
# Connect to database on default 4444 port
make connect-local-client

# Create your first space with engine type key-value
CREATE-SPACE my_data --engine key-value

# Switch to the created space
USE my_data

# Store and retrieve data
PUT user:1 "John Doe"
GET user:1
```

## 📚 Documentation

### Getting Started
- **[Setup Guide](docs/SETUP.md)** - Complete installation and configuration guide
- **[Architecture](docs/ARCHITECTURE.md)** - System architecture and design

### Core Features
- **[Key-Value Engine](docs/KEY_VALUE_ENGINE.md)** - Comprehensive guide to key-value operations
- **[Vector Engine](docs/VECTOR_ENGINE.md)** - Vector search capabilities and FAISS integration
- **[User Management](docs/USER_MANAGEMENT.md)** - Authentication, roles, and permissions

### Administration
- **[Dynamic Connection Limiting](docs/DYNAMIC_CONNECTION_LIMITING.md)** - Runtime connection management
- **[Administration Guide](docs/ADMINISTRATION.md)** - Server administration and monitoring

### Reference
- **[API Reference](docs/API_REFERENCE.md)** - Complete command reference
- **[Troubleshooting](docs/TROUBLESHOOTING.md)** - Common issues and solutions

## 🏗️ Architecture

ShibuDb follows a modular architecture with clear separation of concerns:

- **Storage Engine**: Efficient key-value and vector storage with WAL
- **Query Engine**: Processes and executes database operations
- **Authentication**: Role-based access control system
- **Space Management**: Multi-tenant data organization
- **Indexing**: B-tree and FAISS vector indexes for fast retrieval
- **Connection Management**: Dynamic connection limiting and monitoring
- **Management API**: HTTP endpoints for runtime control

## 🛠️ Development

### Prerequisites

- Go 1.23.0 or later
- FAISS libraries (included in resources/)

### Build and Test

```bash
# Run tests
make test

# Run benchmarks
make benchmark

# Run E2E tests, this requires test server (make start-local-server)
make e2e-test
```

### Local Development

For local development and testing, you can use the following commands:

```bash
# Start the local development server (port 4444)
make start-local-server

# Connect to the local server using the CLI client
make connect-local-client
```

**Default credentials for local development (when using `make start-local-server`):**
- Username: `admin`
- Password: `admin`
- Port: `4444`

**Available CLI Commands:**
- `help` or `?` - Show the full interactive command reference
- `USE <space>` - Switch to a specific space
- `create-space <name> [--engine key-value|vector] [--dimension N] [--index-type TYPE] [--metric METRIC] [--metadata-fields name:type,...]` - Create a new space
- `put <key> <value>` - Store a key-value pair
- `get <key>` - Retrieve a value by key
- `delete <key>` - Delete a key-value pair
- `insert-vector <id> <comma-separated-floats> [--meta key=value,...]` - Insert a vector (optionally with indexed metadata)
- `delete-vector <id>` - Delete a vector (Not supported for index type HNSW)
- `search-topk <comma-separated-floats> <k> [--where <expression>]` - Search for top-k similar vectors (optionally pre-filtered by metadata)
- `range-search <comma-separated-floats> <radius> [--where <expression>]` - Search within a radius (optionally pre-filtered by metadata)
- `create-user` - Create a new user (admin only)
- `list-spaces` - List all available spaces
- `exit` or `quit` - Exit the client

**Example Workflow:**
```bash
# Terminal 1: Start the server
make start-local-server

# Terminal 2: Connect and interact
make connect-local-client

# In the client:
[admin]> create-space mydata
[admin]> USE mydata
[mydata]> put key1 value1
[mydata]> get key1
[mydata]> create-space vectors --engine vector --dimension 128
[mydata]> USE vectors
[vectors]> insert-vector vec1 1.0,2.0,3.0,4.0
[vectors]> search-topk 1.1,2.1,3.1,4.1 5
```

**Metadata Filtering (Flat index):**

Declare indexed metadata fields when creating a `Flat` vector space, attach metadata on
insert, then pre-filter searches with a `--where` expression. This restricts the similarity
search to matching vectors (e.g. per-tenant data) for better recall and speed.

```bash
# Declare indexed fields (name:type, types: string | int | float)
[admin]> create-space products --engine vector --dimension 4 --index-type Flat --metric L2 \
            --metadata-fields user_id:string,category:string,price:float,year:int
[admin]> USE products

# Attach metadata on insert (key=value; numeric values are inferred, quote to force a string)
[products]> insert-vector 1 0.1,0.1,0.1,0.1 --meta user_id=alice,category=books,price=12.5,year=2020
[products]> insert-vector 2 0.2,0.2,0.2,0.2 --meta user_id=bob,category=books,price=40,year=2022
[products]> insert-vector 3 0.15,0.15,0.15,0.15 --meta user_id=alice,category=toys,price=5,year=2023

# Filtered search
[products]> search-topk 0.1,0.1,0.1,0.1 10 --where user_id=alice
[products]> search-topk 0.1,0.1,0.1,0.1 10 --where user_id=alice AND price<10
[products]> search-topk 0.1,0.1,0.1,0.1 10 --where (user_id=alice OR user_id=bob) AND category=books
[products]> search-topk 0.1,0.1,0.1,0.1 10 --where year BETWEEN 2021 AND 2023
[products]> search-topk 0.1,0.1,0.1,0.1 10 --where category IN (books,toys) AND NOT user_id=bob
[products]> range-search 0.1,0.1,0.1,0.1 1.0 --where user_id=alice
```

`--where` expression grammar (keywords are case-insensitive):
- Comparison: `field = value`, `field != value`, `field > value`, `>=`, `<`, `<=`
- Set membership: `field IN (v1, v2, ...)`
- Numeric range: `field BETWEEN low AND high`
- Boolean composition: `AND`, `OR`, `NOT`, and `( ... )` for nesting
- Values: bare words and quoted strings (`'x'` / `"x"`) are strings; numbers are numeric. Comparison/range operators require numeric (`int`/`float`) fields.

Notes:
- `--metadata-fields`, `--meta`, and `--where` are only available on vector spaces created with `--index-type Flat`.
- The `--metadata-fields` and `--meta` lists must not contain spaces (they are comma-separated).

See [Vector Engine — Metadata Filtering](docs/VECTOR_ENGINE.md#metadata-filtering) for the full grammar, internals, and error reference.

## 📦 Installation Options

### From brew
If you prefer using Homebrew on macOS, you can install ShibuDb directly from our tap:

```bash
brew tap shibudb.org/shibudb

# Install ShibuDb
brew install shibudb

# If you already have an older version installed, you can upgrade
brew link shibudb
```

### From Pre-built Packages

Use pre-built packages for current Linux distributions. For older distributions,
use the source installer below so the ShibuDB binary is compiled against the
local system libraries.

**macOS (Apple Silicon):**
```bash
sudo installer -pkg shibudb-{version}-apple_silicon.pkg -target /
```

**Linux (Debian/Ubuntu):**
```bash
# AMD64
sudo dpkg -i shibudb_{version}_amd64.deb

# ARM64
sudo dpkg -i shibudb_{version}_arm64.deb
```

**Linux (RHEL/CentOS):**
```bash
# AMD64
sudo rpm -i shibudb-{version}-1.x86_64.rpm

# ARM64
sudo rpm -i shibudb-{version}-1.aarch64.rpm
```

### Linux Source Installer

Use this option on older Linux distributions or when a pre-built package is not
compatible with your system. The installer downloads the selected ShibuDB source
release, uses the bundled FAISS libraries from `resources/`, builds `shibudb`
locally, installs the binary under `/usr/local/bin`, installs FAISS libraries
under `/usr/local/lib`, and runs `ldconfig`.

```bash
# Install the latest release
curl -fsSL https://raw.githubusercontent.com/shibudb-org/shibudb-server/main/scripts/install-linux.sh | bash

# Install a specific release
curl -fsSL https://raw.githubusercontent.com/shibudb-org/shibudb-server/main/scripts/install-linux.sh | bash -s -- --version {version}
```

The source installer supports Linux `amd64` and `arm64`. It installs build
dependencies with `apt`, `dnf`, or `yum`, and downloads a temporary Go toolchain
if Go 1.23 or later is not already available.

## 🎯 Use Cases

### Key-Value Storage
- **User Sessions**: Store session data with automatic expiration
- **Configuration Management**: Application and system configuration
- **Caching Layer**: High-performance caching for applications
- **Feature Flags**: Dynamic feature toggles and A/B testing

### Vector Search
- **Recommendation Systems**: User and product recommendations
- **Image Search**: Similar image retrieval and classification
- **Text Similarity**: Document search and semantic matching
- **Anomaly Detection**: Identify unusual patterns in data
- **Face Recognition**: Biometric authentication systems

### Multi-Tenant Applications
- **SaaS Platforms**: Isolated data per customer
- **Microservices**: Service-specific data storage
- **Analytics**: Separate spaces for different data types

## 🔧 Management

### Server Management
```bash
# Start server (default listen port 4444; management API on 5444)
sudo shibudb start

# Or choose ports (client and management must differ)
sudo shibudb start --port 9090 --management-port 19090

# Stop server
sudo shibudb stop

shibudb manager --username admin --password admin generate-token

# Check status (admin-only; default management port 5444)
shibudb manager --username admin --password admin status
shibudb manager --port 19090 --username admin --password admin status
# Check status (default management port 5444)
shibudb manager status
shibudb manager --port 19090 status
```

### Runtime Management
```bash
# View connection statistics (admin-only)
shibudb manager --username admin --password admin stats

# Update connection limit (admin-only)
shibudb manager --username admin --password admin limit 2000

# Health check (admin-only)
shibudb manager --username admin --password admin health

# If the server used e.g. --management-port 19090
shibudb manager --port 19090 --username admin --password admin stats
```

### HTTP Management API
```bash
# Get connection status (default management port 5444)
shibudb manager --username admin --password admin generate-token

# Get connection status (default management port 5444; auth required)
curl http://localhost:5444/limit \
  -H "Authorization: Bearer <management_token>"
# Update connection limit (auth required)
curl -X PUT http://localhost:5444/limit \
  -H "Authorization: Bearer <management_token>" \
  -H "Content-Type: application/json" \
  -d '{"limit": 2000}'
```

## 🤝 Contributing

We welcome contributions! Please see our [Contributing Guidelines](CONTRIBUTING.md) for details.

### Development Workflow

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Make your changes and add tests
4. Run the test suite: `make test`
5. Commit your changes: `git commit -m 'Add amazing feature'`
6. Push to the branch: `git push origin feature/amazing-feature`
7. Open a Pull Request

## 📄 License

This project is licensed under the GNU Affero General Public License v3.0 (AGPL-3.0) - see the [LICENSE](LICENSE) file for details.

**Important Note**: This license requires that if you run a modified version of this software on a network server, you must make the source code available to users of that server. This prevents commercial SaaS providers from using this software without open-sourcing their service.

## 🆘 Support

- **Documentation**: [Wiki](https://github.com/shibudb-org/shibudb-server/wiki)
- **Issues**: [GitHub Issues](https://github.com/shibudb-org/shibudb-server/issues)
- **Discussions**: [GitHub Discussions](https://github.com/shibudb-org/shibudb-server/discussions)

## 🙏 Acknowledgments

- [FAISS](https://github.com/facebookresearch/faiss) - Vector similarity search
- [Go B-tree](https://github.com/google/btree) - B-tree implementation
- [Go Crypto](https://golang.org/x/crypto) - Cryptographic functions

---

**ShibuDb** - Fast, reliable, and scalable database with vector search capabilities.

*For detailed information about specific features, please refer to the [documentation](docs/).*
