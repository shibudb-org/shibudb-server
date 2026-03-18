package server

// DefaultMaxConnections is the default maximum number of concurrent connections
// allowed when no explicit limit is configured.
const DefaultMaxConnections int32 = 1000

// DefaultPort is the default TCP listen port when --port is omitted on start/run/connect.
const DefaultPort = "4444"

// DefaultManagementPort is the default HTTP port for the management API (start/run --management-port).
const DefaultManagementPort = "5444"

