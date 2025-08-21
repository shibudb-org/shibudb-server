# ShibuDb

[![Go Version](https://img.shields.io/badge/Go-1.23.0-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-AGPL%203.0-green.svg)](LICENSE)
[![Platforms](https://img.shields.io/badge/Platforms-Linux%20%7C%20macOS-blue.svg)](https://github.com/shibudb.org/shibudb-server)

ShibuDb is a lightweight, embedded database system with vector search capabilities powered by FAISS. It provides high-performance storage and retrieval with support for both traditional key-value operations and advanced vector similarity search.

## ✨ Key Features

- **🔍 Vector Search**: Advanced similarity search using FAISS
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
git clone https://github.com/shibudb.org/shibudb-server.git
cd ShibuDb

# Start the local server on port 4444 with default admin username and password as admin:admin
make start-local-server
```

### Connect and Use

```bash
# Connect to database on default 4444 port
make connect-local-client

# Login with default credentials
Username: admin
Password: admin

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
- **[Administration Guide](docs/ADMINISTRATION.md)** - Server administration and monitoring *(Coming Soon)*

### Reference
- **[API Reference](docs/API_REFERENCE.md)** - Complete command reference *(Coming Soon)*
- **[Troubleshooting](docs/TROUBLESHOOTING.md)** - Common issues and solutions *(Coming Soon)*

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
# Build for current platform
make build

# Run tests
make test

# Run benchmarks
make benchmark

# Run E2E tests
make e2e-test
```

### Development Setup

```bash
# Setup development environment
make dev-setup

# Run all pre-commit checks
make pre-commit
```

### Local Development

For local development and testing, you can use the following commands:

```bash
# Start the local development server (port 4444)
make start-local-server

# Connect to the local server using the CLI client
make connect-local-client

# Interactive development setup (recommended)
make dev
```

**Default credentials for local development:**
- Username: `admin`
- Password: `admin`
- Port: `4444`

**Available CLI Commands:**
- `USE <space>` - Switch to a specific space
- `create-space <name> [--engine key-value|vector] [--dimension N]` - Create a new space
- `put <key> <value>` - Store a key-value pair
- `get <key>` - Retrieve a value by key
- `delete <key>` - Delete a key-value pair
- `insert-vector <id> <comma-separated-floats>` - Insert a vector
- `search-topk <comma-separated-floats> <k>` - Search for top-k similar vectors
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

## 📦 Installation Options

### From Source
```bash
git clone https://github.com/shibudb.org/shibudb-server.git
cd ShibuDb
make build
sudo make install
```

### From Pre-built Packages

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

### Using Docker
```bash
# Build and run
docker build -t shibudb:latest .
docker run -d --name shibudb -p 9090:9090 -p 10090:10090 shibudb:latest
```

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
# Start server
sudo shibudb start 9090

# Stop server
sudo shibudb stop

# Check status
shibudb manager 9090 status
```

### Runtime Management
```bash
# View connection statistics
shibudb manager 9090 stats

# Update connection limit
shibudb manager 9090 limit 2000

# Health check
shibudb manager 9090 health
```

### HTTP Management API
```bash
# Get connection status
curl http://localhost:10090/limit

# Update connection limit
curl -X PUT http://localhost:10090/limit \
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

- **Documentation**: [Wiki](https://github.com/shibudb.org/shibudb-server/wiki)
- **Issues**: [GitHub Issues](https://github.com/shibudb.org/shibudb-server/issues)
- **Discussions**: [GitHub Discussions](https://github.com/shibudb.org/shibudb-server/discussions)

## 🙏 Acknowledgments

- [FAISS](https://github.com/facebookresearch/faiss) - Vector similarity search
- [Go B-tree](https://github.com/google/btree) - B-tree implementation
- [Go Crypto](https://golang.org/x/crypto) - Cryptographic functions

---

**ShibuDb** - Fast, reliable, and scalable embedded database with vector search capabilities.

*For detailed information about specific features, please refer to the [documentation](docs/).*
