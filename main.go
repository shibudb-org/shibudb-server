/*
ShibuDb - Fast, reliable, and scalable database with vector search capabilities.
Copyright (C) 2025 Podcopic Labs

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shibudb.org/shibudb-server/cmd/server"
	"github.com/shibudb.org/shibudb-server/internal/auth"
	"github.com/shibudb.org/shibudb-server/internal/models"
)

type runtimePaths struct {
	// rootDir is the value passed via --data-dir (defaults to ~/.shibudb or XDG).
	// We store actual runtime artifacts under rootDir/{lib,log,run}.
	rootDir string
	libDir  string
	logDir  string
	runDir  string

	authFile string
	logFile  string
	pidFile  string
}

func defaultDataDir() string {
	// Prefer XDG if present, otherwise fall back to ~/.shibudb.
	// This keeps the server fully runnable without sudo by default.
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, "shibudb")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".shibudb")
	}
	// As a last resort, use CWD (still non-root).
	return ".shibudb"
}

func newRuntimePaths(rootDir string) runtimePaths {
	libDir := filepath.Join(rootDir, "lib")
	logDir := filepath.Join(rootDir, "log")
	runDir := filepath.Join(rootDir, "run")
	return runtimePaths{
		rootDir: rootDir,
		libDir:  libDir,
		logDir:  logDir,
		runDir:  runDir,

		// Store config + data under lib/, logs under log/, pid under run/
		authFile: filepath.Join(libDir, "users.json"),
		logFile:  filepath.Join(logDir, "shibudb.log"),
		pidFile:  filepath.Join(runDir, "shibudb.pid"),
	}
}

// Version and BuildTime will be injected at build time via ldflags
var (
	Version   = "unknown"
	BuildTime = "unknown"
)

const (
	green  = "\033[32m"
	blue   = "\033[34m"
	red    = "\033[31m"
	cyan   = "\033[36m"
	yellow = "\033[33m"
	reset  = "\033[0m"
)

// Check if running with sudo privileges
func isRunningAsRoot() bool {
	return os.Geteuid() == 0
}

// isServerRunning checks whether a server process is running by reading pidFilePath.
func isServerRunning(pidFilePath string) (bool, int) {
	if _, err := os.Stat(pidFilePath); err != nil {
		return false, 0
	}

	pidData, err := os.ReadFile(pidFilePath)
	if err != nil {
		return false, 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		return false, 0
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}

	// Try to send signal 0 to check if process exists
	if proc.Signal(syscall.Signal(0)) == nil {
		return true, pid
	}

	return false, 0
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: shibudb [start [flags] | stop | connect [flags] | manager [flags] <command> | --version | --help]")
		return
	}

	switch os.Args[1] {
	case "--version":
		printVersion()

	case "start":
		fs := flag.NewFlagSet("start", flag.ExitOnError)
		dataDir := fs.String("data-dir", defaultDataDir(), "data directory root (stores files under lib/, log/, run/)")
		adminUser := fs.String("admin-user", "", "admin username for initial bootstrap (non-interactive)")
		adminPass := fs.String("admin-password", "", "admin password for initial bootstrap (non-interactive)")
		portFlag := fs.String("port", server.DefaultPort, "TCP port for client connections (1–65535)")
		mgmtPortFlag := fs.String("management-port", server.DefaultManagementPort, "TCP port for the management HTTP API (1–65535; must differ from --port)")
		maxConnFlag := fs.Int("max-connections", int(resolveDefaultMaxConnections()), "maximum number of concurrent connections (default comes from SHIBUDB_MAX_CONNECTIONS if set; persisted limit may override at runtime)")
		fs.Parse(os.Args[2:]) //nolint
		if len(fs.Args()) != 0 {
			fmt.Println("Usage: shibudb start [--data-dir <path>] [--admin-user <u> --admin-password <p>] [--port <n>] [--management-port <n>] [--max-connections <n>]")
			return
		}
		port, err := normalizeListenPort(*portFlag)
		if err != nil {
			fmt.Println("Invalid --port:", err)
			return
		}
		mgmtPort, err := normalizeListenPort(*mgmtPortFlag)
		if err != nil {
			fmt.Println("Invalid --management-port:", err)
			return
		}
		if port == mgmtPort {
			fmt.Println("Error: --port and --management-port must be different.")
			return
		}
		maxConnections := int32(*maxConnFlag)
		if maxConnections <= 0 {
			fmt.Println("Invalid --max-connections value. Must be a positive integer.")
			return
		}
		startServer(port, mgmtPort, maxConnections, newRuntimePaths(*dataDir), *adminUser, *adminPass)

	case "stop":
		fs := flag.NewFlagSet("stop", flag.ExitOnError)
		dataDir := fs.String("data-dir", defaultDataDir(), "data directory root (used to locate the PID file under run/)")
		fs.Parse(os.Args[2:]) //nolint
		stopServer(newRuntimePaths(*dataDir))

	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		dataDir := fs.String("data-dir", defaultDataDir(), "data directory root (stores files under lib/, log/, run/)")
		adminUser := fs.String("admin-user", "", "admin username for initial bootstrap (non-interactive)")
		adminPass := fs.String("admin-password", "", "admin password for initial bootstrap (non-interactive)")
		portFlag := fs.String("port", server.DefaultPort, "TCP port for client connections (1–65535)")
		mgmtPortFlag := fs.String("management-port", server.DefaultManagementPort, "TCP port for the management HTTP API (1–65535; must differ from --port)")
		maxConnFlag := fs.Int("max-connections", int(resolveDefaultMaxConnections()), "maximum number of concurrent connections (default comes from SHIBUDB_MAX_CONNECTIONS if set; persisted limit may override at runtime)")
		fs.Parse(os.Args[2:]) //nolint
		if len(fs.Args()) != 0 {
			fmt.Println("Usage: shibudb run [--data-dir <path>] [--admin-user <u> --admin-password <p>] [--port <n>] [--management-port <n>] [--max-connections <n>]")
			return
		}
		port, err := normalizeListenPort(*portFlag)
		if err != nil {
			fmt.Println("Invalid --port:", err)
			return
		}
		mgmtPort, err := normalizeListenPort(*mgmtPortFlag)
		if err != nil {
			fmt.Println("Invalid --management-port:", err)
			return
		}
		if port == mgmtPort {
			fmt.Println("Error: --port and --management-port must be different.")
			return
		}
		maxConnections := int32(*maxConnFlag)
		if maxConnections <= 0 {
			fmt.Println("Invalid --max-connections value. Must be a positive integer.")
			return
		}
		paths := newRuntimePaths(*dataDir)
		// Pre-bootstrap admin non-interactively if credentials are provided.
		// This ensures the auth file exists before StartServer's own NewAuthManager call.
		if *adminUser != "" && *adminPass != "" {
			if _, err := auth.NewAuthManagerWithBootstrap(paths.authFile, *adminUser, *adminPass); err != nil {
				log.Fatalf("Failed to bootstrap admin: %v", err)
			}
		}
		server.StartServer(port, paths.authFile, maxConnections, paths.libDir, mgmtPort)

	case "connect":
		fs := flag.NewFlagSet("connect", flag.ExitOnError)
		portFlag := fs.String("port", server.DefaultPort, "TCP port of the ShibuDB server (1–65535)")
		username := fs.String("username", "", "username (optional; will prompt if omitted)")
		password := fs.String("password", "", "password (optional; will prompt if omitted)")
		// Shorthands for convenience/backwards habits
		_ = fs.String("user", "", "alias for --username") // parsed below
		_ = fs.String("pass", "", "alias for --password") // parsed below
		fs.Parse(os.Args[2:])                             //nolint
		if len(fs.Args()) != 0 {
			fmt.Println("Usage: shibudb connect [--port <n>] [--username <u> --password <p>]")
			return
		}
		port, err := normalizeListenPort(*portFlag)
		if err != nil {
			fmt.Println("Invalid --port:", err)
			return
		}
		// If user passed aliases, honor them.
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "user" && *username == "" {
				*username = f.Value.String()
			}
			if f.Name == "pass" && *password == "" {
				*password = f.Value.String()
			}
		})
		connectToServer(port, *username, *password)

	case "manager":
		fs := flag.NewFlagSet("manager", flag.ExitOnError)
		mgmtPortFlag := fs.String("port", server.DefaultManagementPort, "management HTTP API port (must match the server’s --management-port; 1–65535)")
		fs.Parse(os.Args[2:]) //nolint
		args := fs.Args()
		if len(args) < 1 {
			fmt.Println("Usage: shibudb manager [--port <n>] <command> [args...]")
			printManagerUsage()
			return
		}
		mgmtPort, err := normalizeListenPort(*mgmtPortFlag)
		if err != nil {
			fmt.Println("Invalid --port:", err)
			return
		}
		handleManagerCommand(mgmtPort, args)

	case "--help":
		printHelp()

	default:
		fmt.Println("Unknown command:", os.Args[1])
	}
}

func normalizeListenPort(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("must not be empty")
	}
	v, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || v < 1 || v > 65535 {
		return "", errors.New("must be an integer between 1 and 65535")
	}
	return strconv.FormatUint(v, 10), nil
}

func resolveDefaultMaxConnections() int32 {
	if raw := strings.TrimSpace(os.Getenv("SHIBUDB_MAX_CONNECTIONS")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 32); err == nil && v > 0 {
			return int32(v)
		}
		fmt.Printf("%sWarning:%s ignoring invalid SHIBUDB_MAX_CONNECTIONS=%q (must be positive integer); using default %d\n",
			yellow, reset, raw, server.DefaultMaxConnections)
	}
	return server.DefaultMaxConnections
}

func printVersion() {
	fmt.Printf("ShibuDB version %s\n", Version)
	fmt.Printf("Build time: %s\n", BuildTime)
	fmt.Printf("Copyright (C) 2025 Podcopic Labs\n")
	fmt.Printf("License: GNU Affero General Public License v3.0\n")
	fmt.Printf("For more information, visit: https://github.com/shibudb.org/shibudb-server\n")
}

func printHelp() {
	defaultConn := resolveDefaultMaxConnections()
	defPort := server.DefaultPort
	defMgmt := server.DefaultManagementPort
	fmt.Printf(`ShibuDB - Lightweight Database
Usage:
  shibudb start [flags]                        Start the ShibuDB server as a background process
  shibudb run [flags]                          Run server in foreground (internal; used by start)
  shibudb stop                                 Stop the ShibuDB background server
  shibudb connect [flags]                      Connect to the ShibuDB CLI client
  shibudb manager [flags] <command>            Manage connection limits at runtime
  shibudb --version                            Show version information
  shibudb --help                               Show this help message

Listen port (start/run/connect):
  Default TCP port: %s (override with --port; must be 1–65535)

Management HTTP API (start/run only):
  Default port: %s (override with --management-port; must differ from --port)

Connection Limits:
  Default maximum concurrent connections: %d (must be a positive integer)
  flags/env:
    --max-connections <n>        Explicit limit for this start/run
    SHIBUDB_MAX_CONNECTIONS=<n>  Default when --max-connections is omitted
  note: persisted connection_limit.json (under --data-dir) may override at runtime

Runtime Management:
  The server includes a management API for dynamic connection limit updates:
  - HTTP API: http://localhost:%s/ (set with start/run --management-port; default %s)
  - Signals: SIGUSR1 (increase by 100), SIGUSR2 (decrease by 100)
  - CLI: shibudb manager [--port <management_port>] <command> (default %s; must match server)

Manager Commands:
  status                    Show current connection limit and active connections
  stats                     Show detailed connection statistics
  limit <new_limit>         Set connection limit to specific value
  increase [amount]         Increase connection limit by amount (default: 100)
  decrease [amount]         Decrease connection limit by amount (default: 100)
  health                    Check server health

Examples:
  shibudb start                        # Default port %s, default connection limit
  shibudb start --port 9090            # Listen on 9090
  SHIBUDB_MAX_CONNECTIONS=2000 shibudb start
                                      # Env connection limit, default port %s
  shibudb start --max-connections 2000 --port 9090
                                      # Custom limit and port
  shibudb start --max-connections 500 --port 9090
  shibudb start --admin-user admin --admin-password admin --port 9090
                                      # First start: bootstrap admin non-interactively
  shibudb connect --username admin --password admin
                                      # Connect to default port %s
  shibudb connect --port 9090 --username admin --password admin
  shibudb manager status                 # Management API on default port %s
  shibudb start --port 9090 --management-port 19090
  shibudb manager --port 19090 limit 2000
                                      # Custom client and management ports
  shibudb manager increase 500
  kill -USR1 <pid>                     # Increase limit by 100 via signal

Note: By default, ShibuDB stores runtime files under your home directory.
You can override paths with --data-dir.
`, defPort, defMgmt, defaultConn, defMgmt, defMgmt, defMgmt, defPort, defPort, defPort, defMgmt)
}

func connectToServer(port, providedUser, providedPass string) {
	conn, err := net.Dial("tcp", "localhost:"+port)
	if err != nil {
		fmt.Printf("Failed to connect to server: %v\n", err)
		return
	}
	defer conn.Close()

	reader := bufio.NewReader(os.Stdin)
	serverReader := bufio.NewReader(conn)

	// --- Login Prompt ---
	username := strings.TrimSpace(providedUser)
	password := strings.TrimSpace(providedPass)
	if username == "" {
		username = readLine("Username: ", reader)
	}
	if password == "" {
		password = readLine("Password: ", reader)
	}

	login := models.LoginRequest{Username: username, Password: password}
	data, _ := json.Marshal(login)
	conn.Write(append(data, '\n'))

	resp, err := serverReader.ReadString('\n')
	if err != nil || !strings.Contains(resp, `"status":"OK"`) {
		fmt.Println("Authentication failed. Server response:", strings.TrimSpace(resp))
		return
	}
	fmt.Println("Login successful.")

	var currentUser models.User
	respBody := make(map[string]interface{})
	_ = json.Unmarshal([]byte(resp), &respBody)

	if u, ok := respBody["user"].(map[string]interface{}); ok {
		jsonUser, _ := json.Marshal(u)
		_ = json.Unmarshal(jsonUser, &currentUser)
	}

	var space string
	space = ""

	// --- Command loop ---
	for {
		fmt.Printf("[%s]> ", space)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}
		if strings.EqualFold(line, "exit") || strings.EqualFold(line, "quit") {
			fmt.Println("Goodbye!")
			break
		}

		if strings.HasPrefix(strings.ToUpper(line), "USE ") {
			querySpace := strings.TrimSpace(line[4:])
			useQuery := models.Query{Type: models.TypeUseSpace, Space: querySpace, User: username}
			data, _ := json.Marshal(useQuery)
			conn.Write(append(data, '\n'))
			useResponse, err := serverReader.ReadString('\n')
			if err != nil || !strings.Contains(useResponse, `"status":"OK"`) {
				printResponse(useResponse)
				continue
			}
			space = querySpace
			printResponse(useResponse)
			continue
		}

		parts := strings.Fields(line)

		var commandsRequiringSpace = map[string]bool{
			"put":    true,
			"get":    true,
			"delete": true,
		}
		if commandsRequiringSpace[strings.ToLower(parts[0])] && space == "" {
			fmt.Println("No space selected. Use 'USE <space>' first.")
			continue
		}

		var query models.Query
		switch strings.ToLower(parts[0]) {
		case "create-user":
			if currentUser.Role != auth.RoleAdmin {
				fmt.Println("Only admin can add users.")
				continue
			}
			newUserData := promptNewUser(reader)
			query = models.Query{
				Type:    models.TypeCreateUser,
				User:    currentUser.Username,
				NewUser: &newUserData,
			}
		case "update-user-role":
			if len(parts) < 2 {
				fmt.Println("Usage: update-user-role <username>")
				continue
			}

			username := parts[1]

			if currentUser.Role != auth.RoleAdmin {
				fmt.Println("Only admin can update users.")
				continue
			}
			user := promptUpdateUserRole(reader, username)
			query = models.Query{
				Type:    models.TypeUpdateUserRole,
				User:    currentUser.Username,
				NewUser: &user,
			}
		case "update-user-password":
			if len(parts) < 2 {
				fmt.Println("Usage: update-user-password <username>")
				continue
			}

			username := parts[1]

			if currentUser.Role != auth.RoleAdmin {
				fmt.Println("Only admin can update users.")
				continue
			}
			user := promptUpdateUserPassword(reader, username)
			query = models.Query{
				Type:    models.TypeUpdateUserPassword,
				User:    currentUser.Username,
				NewUser: &user,
			}
		case "update-user-permissions":
			if len(parts) < 2 {
				fmt.Println("Usage: update-user-permissions <username>")
				continue
			}

			username := parts[1]

			if currentUser.Role != auth.RoleAdmin {
				fmt.Println("Only admin can update users.")
				continue
			}
			user := promptUpdateUserPermissions(reader, username)
			query = models.Query{
				Type:    models.TypeUpdateUserPermissions,
				User:    currentUser.Username,
				NewUser: &user,
			}
		case "delete-user":
			if len(parts) < 2 {
				fmt.Println("Usage: delete-user <username>")
				continue
			}

			user := models.User{
				Username: parts[1],
			}

			query = models.Query{Type: models.TypeDeleteUser, DeleteUser: &user}
		case "get-user":
			if len(parts) < 2 {
				fmt.Println("Usage: get-user <username>")
				continue
			}

			query = models.Query{Type: models.TypeGetUser, Data: parts[1]}
		case "create-space":
			if len(parts) < 2 {
				fmt.Println("Usage: create-space <name> [--engine key-value|vector] [--dimension N] [--index-type TYPE] [--metric METRIC] [--enable-wal] [--disable-wal]")
				continue
			}
			engineType := "key-value"
			dimension := 0
			indexType := "Flat"
			metric := "L2"
			enableWAL := false // Will be set based on engine type
			walExplicitlySet := false
			for i := 2; i < len(parts); i++ {
				if parts[i] == "--engine" && i+1 < len(parts) {
					engineType = parts[i+1]
					i++
				} else if parts[i] == "--dimension" && i+1 < len(parts) {
					dim, err := strconv.Atoi(parts[i+1])
					if err == nil {
						dimension = dim
					}
					i++
				} else if parts[i] == "--index-type" && i+1 < len(parts) {
					indexType = parts[i+1]
					i++
				} else if parts[i] == "--metric" && i+1 < len(parts) {
					metricStr := parts[i+1]
					metric = metricStr
					i++
				} else if parts[i] == "--enable-wal" {
					enableWAL = true
					walExplicitlySet = true
				} else if parts[i] == "--disable-wal" {
					enableWAL = false
					walExplicitlySet = true
				}
			}

			// Set default WAL based on engine type if not explicitly set
			if !walExplicitlySet {
				enableWAL = (engineType == "key-value") // Default to WAL enabled for key-value, disabled for vector
			}

			if engineType == "vector" && dimension <= 0 {
				fmt.Println("For vector engine, you must specify --dimension <N> (e.g., 128)")
				continue
			}
			query = models.Query{Type: models.TypeCreateSpace, Space: parts[1], User: username, EngineType: engineType, Dimension: dimension, IndexType: indexType, Metric: metric, EnableWAL: enableWAL}
		case "delete-space":
			if len(parts) < 2 {
				fmt.Println("Usage: delete-space <name>")
				continue
			}
			query = models.Query{Type: models.TypeDeleteSpace, Data: parts[1], User: username}
		case "list-spaces":
			query = models.Query{Type: models.TypeListSpaces, User: username}
		case "put":
			if len(parts) < 3 {
				fmt.Println("Usage: put <key> <value>")
				continue
			}
			query = models.Query{Type: models.TypePut, Key: parts[1], Value: parts[2], Space: space, User: username}
		case "get":
			query = models.Query{Type: models.TypeGet, Key: parts[1], Space: space, User: username}
		case "delete":
			query = models.Query{Type: models.TypeDelete, Key: parts[1], Space: space, User: username}
		case "insert-vector":
			if space == "" {
				fmt.Println("No space selected. Use 'USE <space>' first.")
				continue
			}
			if len(parts) < 3 {
				fmt.Println("Usage: insert-vector <id> <comma-separated-floats>")
				continue
			}
			query = models.Query{Type: models.TypeInsertVector, Key: parts[1], Value: parts[2], Space: space, User: username}
		case "delete-vector":
			if space == "" {
				fmt.Println("No space selected. Use 'USE <space>' first.")
				continue
			}
			if len(parts) < 2 {
				fmt.Println("Usage: delete-vector <id>")
				continue
			}
			query = models.Query{Type: models.TypeDeleteVector, Key: parts[1], Space: space, User: username}
		case "search-topk":
			if space == "" {
				fmt.Println("No space selected. Use 'USE <space>' first.")
				continue
			}
			if len(parts) < 3 {
				fmt.Println("Usage: search-topk <comma-separated-floats> <k>")
				continue
			}
			k, err := strconv.Atoi(parts[2])
			if err != nil || k <= 0 {
				fmt.Println("Invalid value for k")
				continue
			}
			query = models.Query{Type: models.TypeSearchTopK, Value: parts[1], Space: space, User: username, Dimension: k}
		case "get-vector":
			if space == "" {
				fmt.Println("No space selected. Use 'USE <space>' first.")
				continue
			}
			if len(parts) < 2 {
				fmt.Println("Usage: get-vector <id>")
				continue
			}
			query = models.Query{Type: models.TypeGetVector, Key: parts[1], Space: space, User: username}
		case "range-search":
			if space == "" {
				fmt.Println("No space selected. Use 'USE <space>' first.")
				continue
			}
			if len(parts) < 3 {
				fmt.Println("Usage: range-search <comma-separated-floats> <radius>")
				continue
			}
			radius, err := strconv.ParseFloat(parts[2], 32)
			if err != nil {
				fmt.Println("Invalid value for radius")
				continue
			}
			query = models.Query{Type: models.TypeRangeSearch, Value: parts[1], Space: space, User: username, Radius: float32(radius)}
		default:
			fmt.Println("Unknown command:", parts[0])
			continue
		}

		data, _ = json.Marshal(query)
		conn.Write(append(data, '\n'))

		resp, err = serverReader.ReadString('\n')
		if err != nil {
			fmt.Println("Server response error:", err)
			break
		}
		printResponse(strings.TrimSpace(resp))
	}
}

func printResponse(resp string) {
	resp = strings.TrimSpace(resp)

	var parsed map[string]interface{}
	err := json.Unmarshal([]byte(resp), &parsed)
	if err != nil {
		// Fallback for non-JSON or malformed responses
		fmt.Println(resp)
		return
	}

	status := strings.ToUpper(parsed["status"].(string))
	switch status {
	case "OK":
		fmt.Print(green)
		if msg, ok := parsed["message"]; ok {
			fmt.Printf("Success: %v\n", msg)
		}
		if val, ok := parsed["value"]; ok {
			fmt.Printf("Value: %v\n", val)
		}
		fmt.Print(reset)
	default:
		if msg, ok := parsed["message"]; ok {
			fmt.Printf("%sError:%s %v\n", red, reset, msg)
		} else {
			fmt.Printf("%sError%s\n", red, reset)
		}
	}
}

func readLine(prompt string, reader *bufio.Reader) string {
	fmt.Print(prompt)
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

func promptNewUser(reader *bufio.Reader) models.User {
	fmt.Print("New Username: ")
	uname, _ := reader.ReadString('\n')
	uname = strings.TrimSpace(uname)

	fmt.Print("New Password: ")
	pass, _ := reader.ReadString('\n')
	pass = strings.TrimSpace(pass)

	fmt.Print("Role (admin/user): ")
	role, _ := reader.ReadString('\n')
	role = strings.TrimSpace(role)

	permissions := map[string]string{}
	if role != auth.RoleAdmin {
		fmt.Println("Enter table permissions (e.g., table1=read or table2=write). Leave blank to finish:")
		for {
			fmt.Print("Permission: ")
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				permissions[parts[0]] = parts[1]
			} else {
				fmt.Println("Invalid format. Use table=role")
			}
		}
	}

	return models.User{
		Username:    uname,
		Password:    pass,
		Role:        role,
		Permissions: permissions,
	}
}

func promptUpdateUserPermissions(reader *bufio.Reader, username string) models.User {
	permissions := map[string]string{}
	fmt.Println("Enter table permissions (e.g., table1=read or table2=write). Leave blank to finish:")
	for {
		fmt.Print("Permission: ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			permissions[parts[0]] = parts[1]
		} else {
			fmt.Println("Invalid format. Use table=role")
		}
	}

	return models.User{
		Username:    username,
		Permissions: permissions,
	}
}

func promptUpdateUserPassword(reader *bufio.Reader, username string) models.User {
	fmt.Print("New Password: ")
	pass, _ := reader.ReadString('\n')
	pass = strings.TrimSpace(pass)

	return models.User{
		Username: username,
		Password: pass,
	}
}

func promptUpdateUserRole(reader *bufio.Reader, username string) models.User {
	fmt.Print("Role (admin/user): ")
	role, _ := reader.ReadString('\n')
	role = strings.TrimSpace(role)

	return models.User{
		Username: username,
		Role:     role,
	}
}

func printStartupBanner() {
	fmt.Println(green + `
  ____  _     _  _             ____  ____  
 / ___|| |__ (_)| |__   _   _ |  _ \| __ ) 
 \___ \| '_ \| || '_ \ | | | || | | |  _ \ 
  ___) | | | | || |_) || |_| || |_| | |_) |
 |____/|_| |_|_||_.__/  \___/ |____/|____/  
` + cyan + `Secure | Fast — Welcome to ShibuDB` + reset)

	fmt.Printf("%sVersion:%s %s\n", blue, reset, Version)
	fmt.Printf("%sDocs   :%s https://github.com/shibudb.org/shibudb-server\n", blue, reset)
}

// buildRunSubcommandArgs builds argv for the child `run` process invoked by start.
func buildRunSubcommandArgs(port, defaultPort, mgmtPort, defaultMgmtPort string, maxConnections, defaultLimit int32, paths runtimePaths, adminUser, adminPass string) []string {
	cmdArgs := []string{"run", "--data-dir", paths.rootDir}
	if adminUser != "" {
		cmdArgs = append(cmdArgs, "--admin-user", adminUser, "--admin-password", adminPass)
	}
	if port != defaultPort {
		cmdArgs = append(cmdArgs, "--port", port)
	}
	if mgmtPort != defaultMgmtPort {
		cmdArgs = append(cmdArgs, "--management-port", mgmtPort)
	}
	if maxConnections != defaultLimit {
		cmdArgs = append(cmdArgs, "--max-connections", strconv.FormatInt(int64(maxConnections), 10))
	}
	return cmdArgs
}

func startServer(port, mgmtPort string, maxConnections int32, paths runtimePaths, adminUser, adminPass string) {
	// Check if server is already running
	if running, pid := isServerRunning(paths.pidFile); running {
		fmt.Printf("%sError:%s ShibuDB server is already running (PID: %d)\n", red, reset, pid)
		fmt.Printf("Use 'shibudb stop' (or specify --data-dir) to stop the existing server first.\n")
		os.Exit(1)
	}

	_, err := auth.NewAuthManagerWithBootstrap(paths.authFile, adminUser, adminPass)
	if err != nil {
		log.Fatalf("Failed to initialize auth manager: %v", err)
	}
	printStartupBanner()

	cmdArgs := buildRunSubcommandArgs(port, server.DefaultPort, mgmtPort, server.DefaultManagementPort, maxConnections, resolveDefaultMaxConnections(), paths, adminUser, adminPass)
	cmd := exec.Command(os.Args[0], cmdArgs...)

	logFile := openLogFile(paths.logFile)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	err = cmd.Start()
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	// Wait a moment to see if the process starts successfully
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Check if process started successfully within 2 seconds
	select {
	case err := <-done:
		if err != nil {
			log.Fatalf("Server failed to start: %v", err)
		}
	case <-time.After(2 * time.Second):
		// Process is still running, which is good
	}

	// Create PID file directory and write PID
	pidDir := filepath.Dir(paths.pidFile)
	err = os.MkdirAll(pidDir, 0755)
	if err != nil {
		log.Fatalf("Failed to create PID directory: %v", err)
	}

	err = os.WriteFile(paths.pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
	if err != nil {
		log.Fatalf("Failed to write PID file: %v", err)
	}

	displayLimit := maxConnections
	if pl, err := server.LoadConnectionLimit(paths.libDir); err == nil && pl > 0 {
		displayLimit = pl
		if pl != maxConnections {
			fmt.Printf("%sNote: Persisted connection limit %d overrides the limit passed to this start (%d).%s\n", yellow, pl, maxConnections, reset)
		}
	} else if err != nil && !os.IsNotExist(err) {
		fmt.Printf("%sWarning: Could not read persisted connection limit: %v%s\n", yellow, err, reset)
	}

	fmt.Printf("%sShibuDB started on port %s (PID: %d, max connections: %d)%s\n", green, port, cmd.Process.Pid, displayLimit, reset)
}

func stopServer(paths runtimePaths) {
	// Check if server is running
	if running, pid := isServerRunning(paths.pidFile); !running {
		fmt.Printf("%sError:%s ShibuDB server is not running.\n", red, reset)
		os.Exit(1)
	} else {
		proc, err := os.FindProcess(pid)
		if err != nil {
			fmt.Printf("%sError:%s Failed to find process %d: %v\n", red, reset, pid, err)
			os.Exit(1)
		}

		err = proc.Kill()
		if err != nil {
			fmt.Printf("%sError:%s Failed to kill process %d: %v\n", red, reset, pid, err)
			os.Exit(1)
		}

		os.Remove(paths.pidFile)
		fmt.Printf("%sShibuDB stopped (PID: %d).%s\n", green, pid, reset)
	}
}

func handleManagerCommand(managementPort string, args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: shibudb manager [--port <n>] <command> [args...]")
		printManagerUsage()
		return
	}

	command := args[0]
	baseURL := fmt.Sprintf("http://localhost:%s", managementPort)

	// Test connectivity first
	if !testManagementConnectivity(baseURL) {
		fmt.Printf("Error: Cannot connect to management server at %s\n", baseURL)
		fmt.Printf("Please ensure the server is running and the management port is accessible.\n")
		return
	}

	switch command {
	case "status":
		getManagerStatus(baseURL)
	case "stats":
		getManagerStats(baseURL)
	case "limit":
		if len(args) < 2 {
			fmt.Println("Usage: shibudb manager [--port <n>] limit <new_limit>")
			return
		}
		newLimit, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Printf("Error: Invalid limit value: %s\n", args[1])
			return
		}
		setManagerLimit(baseURL, int32(newLimit))
	case "increase":
		amount := 100
		if len(args) >= 2 {
			if amt, err := strconv.Atoi(args[1]); err == nil {
				amount = amt
			}
		}
		increaseManagerLimit(baseURL, int32(amount))
	case "decrease":
		amount := 100
		if len(args) >= 2 {
			if amt, err := strconv.Atoi(args[1]); err == nil {
				amount = amt
			}
		}
		decreaseManagerLimit(baseURL, int32(amount))
	case "health":
		checkManagerHealth(baseURL)
	case "reset":
		resetManagerLimit(baseURL)
	default:
		fmt.Printf("Error: Unknown command: %s\n", command)
		printManagerUsage()
	}
}

func testManagementConnectivity(baseURL string) bool {
	fmt.Printf("Testing connectivity to management server...\n")

	// First test if the port is listening
	port := strings.TrimPrefix(baseURL, "http://localhost:")
	conn, err := net.DialTimeout("tcp", "localhost:"+port, 3*time.Second)
	if err != nil {
		fmt.Printf("Port connectivity test failed: %v\n", err)
		fmt.Printf("Management port %s is not accessible\n", port)
		return false
	}
	conn.Close()
	fmt.Printf("✓ Port %s is listening\n", port)

	// Now test HTTP connectivity
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(baseURL + "/health")
	if err != nil {
		fmt.Printf("HTTP connectivity test failed: %v\n", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		fmt.Printf("✓ Management server is accessible\n")
		return true
	} else {
		fmt.Printf("✗ Management server returned status: %s\n", resp.Status)
		return false
	}
}

func printManagerUsage() {
	fmt.Printf(`Manager Commands:
  status                    Show current connection limit and active connections
  stats                     Show detailed connection statistics
  limit <new_limit>         Set connection limit to specific value
  increase [amount]         Increase connection limit by amount (default: 100)
  decrease [amount]         Decrease connection limit by amount (default: 100)
  health                    Check server health
  reset                     Reset connection limit to configured default

Usage:
  shibudb manager [--port <management_port>] <command> [args...]
  Default --port is %s (must match the server’s --management-port).

Examples:
  shibudb manager status
  shibudb manager limit 2000
  shibudb manager increase 500
  shibudb manager decrease 200
  shibudb manager reset
  shibudb manager stats
  shibudb manager --port 19090 limit 2000   # when server uses --management-port 19090
`, server.DefaultManagementPort)
}

func makeManagerRequest(method, url string, body interface{}) (*http.Response, error) {
	var req *http.Request
	var err error

	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		req, err = http.NewRequest(method, url, bytes.NewBuffer(jsonBody))
	} else {
		req, err = http.NewRequest(method, url, nil)
	}

	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	// Add timeout to prevent infinite wait
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	fmt.Printf("Making request to: %s %s\n", method, url)
	return client.Do(req)
}

func getManagerStatus(baseURL string) {
	resp, err := makeManagerRequest("GET", baseURL+"/limit", nil)
	if err != nil {
		fmt.Printf("Error: Failed to connect to management server: %v\n", err)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("Error: Failed to parse response: %v\n", err)
		return
	}

	fmt.Printf("Connection Status:\n")
	fmt.Printf("Current Limit: %d\n", int(result["current_limit"].(float64)))
	fmt.Printf("Active Connections: %d\n", int(result["active_connections"].(float64)))
}

func getManagerStats(baseURL string) {
	fmt.Printf("Connecting to management server at: %s\n", baseURL)

	resp, err := makeManagerRequest("GET", baseURL+"/stats", nil)
	if err != nil {
		fmt.Printf("Error: Failed to connect to management server: %v\n", err)
		fmt.Printf("Please check if the server is running and the management port is accessible.\n")
		fmt.Printf("Management server should be running on port: %s\n", strings.TrimPrefix(baseURL, "http://localhost:"))
		return
	}
	defer resp.Body.Close()

	fmt.Printf("Response status: %s\n", resp.Status)

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("Error: Failed to parse response: %v\n", err)
		return
	}

	fmt.Printf("Connection Statistics:\n")
	fmt.Printf("Active Connections: %d\n", int(result["active_connections"].(float64)))
	fmt.Printf("Max Connections: %d\n", int(result["max_connections"].(float64)))
	fmt.Printf("Usage Percentage: %.1f%%\n", result["usage_percentage"].(float64))
	fmt.Printf("Available Slots: %d\n", int(result["available_slots"].(float64)))
}

func setManagerLimit(baseURL string, newLimit int32) {
	body := map[string]interface{}{
		"limit": newLimit,
	}

	resp, err := makeManagerRequest("PUT", baseURL+"/limit", body)
	if err != nil {
		fmt.Printf("Error: Failed to connect to management server: %v\n", err)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("Error: Failed to parse response: %v\n", err)
		return
	}

	if resp.StatusCode == http.StatusOK {
		fmt.Printf("Success: %s\n", result["message"])
	} else {
		fmt.Printf("Error: %s\n", result["error"])
	}
}

func increaseManagerLimit(baseURL string, amount int32) {
	body := map[string]interface{}{
		"amount": amount,
	}

	resp, err := makeManagerRequest("POST", baseURL+"/limit/increase", body)
	if err != nil {
		fmt.Printf("Error: Failed to connect to management server: %v\n", err)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("Error: Failed to parse response: %v\n", err)
		return
	}

	if resp.StatusCode == http.StatusOK {
		fmt.Printf("Success: %s\n", result["message"])
		fmt.Printf("Old Limit: %d, New Limit: %d\n",
			int(result["old_limit"].(float64)), int(result["new_limit"].(float64)))
	} else {
		fmt.Printf("Error: %s\n", result["error"])
	}
}

func decreaseManagerLimit(baseURL string, amount int32) {
	body := map[string]interface{}{
		"amount": amount,
	}

	resp, err := makeManagerRequest("POST", baseURL+"/limit/decrease", body)
	if err != nil {
		fmt.Printf("Error: Failed to connect to management server: %v\n", err)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("Error: Failed to parse response: %v\n", err)
		return
	}

	if resp.StatusCode == http.StatusOK {
		fmt.Printf("Success: %s\n", result["message"])
		fmt.Printf("Old Limit: %d, New Limit: %d\n",
			int(result["old_limit"].(float64)), int(result["new_limit"].(float64)))
	} else {
		fmt.Printf("Error: %s\n", result["error"])
	}
}

func checkManagerHealth(baseURL string) {
	resp, err := makeManagerRequest("GET", baseURL+"/health", nil)
	if err != nil {
		fmt.Printf("Error: Failed to connect to management server: %v\n", err)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("Error: Failed to parse response: %v\n", err)
		return
	}

	if resp.StatusCode == http.StatusOK {
		fmt.Printf("Health Check: %s\n", result["status"])
		fmt.Printf("Service: %s\n", result["service"])
	} else {
		fmt.Printf("Error: Health check failed\n")
	}
}

func resetManagerLimit(baseURL string) {
	// Reset to configured default limit.
	defaultLimit := resolveDefaultMaxConnections()
	body := map[string]interface{}{
		"limit": defaultLimit,
	}

	resp, err := makeManagerRequest("PUT", baseURL+"/limit", body)
	if err != nil {
		fmt.Printf("Error: Failed to connect to management server: %v\n", err)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("Error: Failed to parse response: %v\n", err)
		return
	}

	if resp.StatusCode == http.StatusOK {
		fmt.Printf("Success: Reset connection limit to default (%d)\n", defaultLimit)
	} else {
		fmt.Printf("Error: %s\n", result["error"])
	}
}

func openLogFile(logFilePath string) *os.File {
	logDir := filepath.Dir(logFilePath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatalf("Unable to create log directory %s: %v", logDir, err)
	}

	f, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Unable to open log file %s: %v", logFilePath, err)
	}
	return f
}
