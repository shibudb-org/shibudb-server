/*
ShibuDb - Fast, reliable, and scalable database with vector search capabilities.
Copyright (C) 2026 Podcopic Labs

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
	"io"
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
	"github.com/shibudb.org/shibudb-server/internal/cliinput"
	"github.com/shibudb.org/shibudb-server/internal/models"
	"github.com/shibudb.org/shibudb-server/internal/spaces"
)

type runtimePaths struct {
	// rootDir is the value passed via --data-dir (defaults to ~/.shibudb or XDG).
	// We store actual runtime artifacts under rootDir/{lib,log,run}.
	rootDir string
	libDir  string
	logDir  string
	runDir  string

	authFile  string
	tokenFile string
	logFile   string
	pidFile   string
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
		authFile:  filepath.Join(libDir, "users.json"),
		tokenFile: filepath.Join(libDir, "management_tokens.json"),
		logFile:   filepath.Join(logDir, "shibudb.log"),
		pidFile:   filepath.Join(runDir, "shibudb.pid"),
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
		fmt.Println("Usage: shibudb [start [flags] | stop | connect [flags] | manager [flags] <command> | rebuild-index [flags] <space_name> | --version | --help]")
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
		dataDir := fs.String("data-dir", defaultDataDir(), "data directory root (used to locate auth and token files under lib/)")
		username := fs.String("username", "", "admin username (optional; will prompt if omitted)")
		password := fs.String("password", "", "admin password (optional; will prompt if omitted)")
		_ = fs.String("user", "", "alias for --username")
		_ = fs.String("pass", "", "alias for --password")
		fs.Parse(os.Args[2:]) //nolint
		args := fs.Args()
		if len(args) < 1 {
			fmt.Println("Usage: shibudb manager [--port <n>] [--data-dir <path>] [--username <u> --password <p>] <command> [args...]")
			printManagerUsage()
			return
		}
		mgmtPort, err := normalizeListenPort(*mgmtPortFlag)
		if err != nil {
			fmt.Println("Invalid --port:", err)
			return
		}
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "user" && *username == "" {
				*username = f.Value.String()
			}
			if f.Name == "pass" && *password == "" {
				*password = f.Value.String()
			}
		})
		paths := newRuntimePaths(*dataDir)
		authCfg := managerAuthConfig{
			username:  strings.TrimSpace(*username),
			password:  strings.TrimSpace(*password),
			authFile:  paths.authFile,
			tokenFile: paths.tokenFile,
		}
		handleManagerCommand(mgmtPort, args, authCfg)

	case "rebuild-index":
		fs := flag.NewFlagSet("rebuild-index", flag.ExitOnError)
		dataDir := fs.String("data-dir", defaultDataDir(), "data directory root (used to locate space files under lib/)")
		fs.Parse(os.Args[2:]) //nolint
		if len(fs.Args()) != 1 {
			fmt.Println("Usage: shibudb rebuild-index [--data-dir <path>] <space_name>")
			return
		}
		rebuildIndex(newRuntimePaths(*dataDir), fs.Args()[0])

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
	fmt.Printf("Copyright (C) 2026 Podcopic Labs\n")
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
  shibudb rebuild-index [flags] <space_name>   Rebuild one space index from on-disk data
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
  - CLI: shibudb manager [--port <management_port>] [--username <u> --password <p>] <command> (default %s; must match server)

Manager Commands:
  status                    Show current connection limit and active connections
  stats                     Show detailed connection statistics
  update-space-settings     [--segment-rollover-bytes N] [--max-segments-before-merge N] <space>
  limit <new_limit>         Set connection limit to specific value
  increase [amount]         Increase connection limit by amount (default: 100)
  decrease [amount]         Decrease connection limit by amount (default: 100)
  health                    Check server health
  generate-token            Generate a management bearer token
  list-tokens               List stored management tokens
  delete-token <token_id>   Delete a management token

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
  shibudb manager --username admin --password admin status   # Management API on default port %s
  shibudb manager --username admin --password admin update-space-settings --segment-rollover-bytes 52428800 my_space
  shibudb start --port 9090 --management-port 19090
  shibudb manager --port 19090 --username admin --password admin limit 2000
                                      # Custom client and management ports
  shibudb manager --username admin --password admin increase 500
  shibudb manager --username admin --password admin generate-token
  shibudb rebuild-index yd_v_global
                                      # Rebuild one space index while the server is stopped
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

	input, err := cliinput.New(os.Stdin, os.Stdout)
	if err != nil {
		fmt.Printf("Failed to initialize CLI input: %v\n", err)
		return
	}
	defer input.Close()

	serverReader := bufio.NewReader(conn)

	// --- Login Prompt ---
	username := strings.TrimSpace(providedUser)
	password := strings.TrimSpace(providedPass)
	if username == "" {
		username, err = readLine("Username: ", input)
		if err != nil {
			fmt.Printf("Failed to read username: %v\n", err)
			return
		}
	}
	if password == "" {
		password, err = readPassword("Password: ", input)
		if err != nil {
			fmt.Printf("Failed to read password: %v\n", err)
			return
		}
	}

	login := models.LoginRequest{Username: username, Password: password}
	if err := sendJSONLine(conn, login); err != nil {
		fmt.Printf("Failed to send login request: %v\n", err)
		return
	}

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
		line, readErr := readLine(fmt.Sprintf("[%s]> ", space), input)
		if readErr != nil {
			fmt.Printf("Input error: %v\n", readErr)
			break
		}
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		input.AppendHistory(line)

		if strings.EqualFold(line, "exit") || strings.EqualFold(line, "quit") {
			fmt.Println("Goodbye!")
			break
		}

		if strings.HasPrefix(strings.ToUpper(line), "USE ") {
			querySpace := strings.TrimSpace(line[4:])
			useQuery := models.Query{Type: models.TypeUseSpace, Space: querySpace, User: username}
			if err := sendJSONLine(conn, useQuery); err != nil {
				fmt.Printf("Failed to send USE command: %v\n", err)
				break
			}
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
			newUserData, err := promptNewUser(input)
			if err != nil {
				fmt.Printf("Failed to read new user input: %v\n", err)
				continue
			}
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
			user, err := promptUpdateUserRole(input, username)
			if err != nil {
				fmt.Printf("Failed to read role update input: %v\n", err)
				continue
			}
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
			user, err := promptUpdateUserPassword(input, username)
			if err != nil {
				fmt.Printf("Failed to read password update input: %v\n", err)
				continue
			}
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
			user, err := promptUpdateUserPermissions(input, username)
			if err != nil {
				fmt.Printf("Failed to read permissions update input: %v\n", err)
				continue
			}
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
				fmt.Println("Usage: create-space <name> [--engine key-value|vector] [--dimension N] [--index-type TYPE] [--metric METRIC] [--enable-wal] [--disable-wal] [--segment-rollover-bytes N] [--max-segments-before-merge N]")
				continue
			}
			engineType := "key-value"
			dimension := 0
			indexType := "Flat"
			metric := "L2"
			enableWAL := false // Will be set based on engine type
			walExplicitlySet := false
			segmentRolloverBytes := int64(0)
			maxSegmentsBeforeMerge := 0
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
				} else if parts[i] == "--segment-rollover-bytes" && i+1 < len(parts) {
					value, err := strconv.ParseInt(parts[i+1], 10, 64)
					if err == nil {
						segmentRolloverBytes = value
					}
					i++
				} else if parts[i] == "--max-segments-before-merge" && i+1 < len(parts) {
					value, err := strconv.Atoi(parts[i+1])
					if err == nil {
						maxSegmentsBeforeMerge = value
					}
					i++
				}
			}

			// WAL stays off unless the user explicitly enables it.
			if !walExplicitlySet {
				enableWAL = false
			}

			if engineType == "vector" && dimension <= 0 {
				fmt.Println("For vector engine, you must specify --dimension <N> (e.g., 128)")
				continue
			}
			query = models.Query{
				Type:                   models.TypeCreateSpace,
				Space:                  parts[1],
				User:                   username,
				EngineType:             engineType,
				Dimension:              dimension,
				IndexType:              indexType,
				Metric:                 metric,
				EnableWAL:              enableWAL,
				SegmentRolloverBytes:   segmentRolloverBytes,
				MaxSegmentsBeforeMerge: maxSegmentsBeforeMerge,
			}
		case "update-space-settings":
			if len(parts) < 2 {
				fmt.Println("Usage: update-space-settings <name> [--segment-rollover-bytes N] [--max-segments-before-merge N]")
				continue
			}
			segmentRolloverBytes := int64(0)
			maxSegmentsBeforeMerge := 0
			for i := 2; i < len(parts); i++ {
				if parts[i] == "--segment-rollover-bytes" && i+1 < len(parts) {
					value, err := strconv.ParseInt(parts[i+1], 10, 64)
					if err == nil {
						segmentRolloverBytes = value
					}
					i++
				} else if parts[i] == "--max-segments-before-merge" && i+1 < len(parts) {
					value, err := strconv.Atoi(parts[i+1])
					if err == nil {
						maxSegmentsBeforeMerge = value
					}
					i++
				}
			}
			query = models.Query{
				Type:                   models.TypeUpdateSpaceSettings,
				Space:                  parts[1],
				User:                   username,
				SegmentRolloverBytes:   segmentRolloverBytes,
				MaxSegmentsBeforeMerge: maxSegmentsBeforeMerge,
			}
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

		if err := sendJSONLine(conn, query); err != nil {
			fmt.Printf("Failed to send command: %v\n", err)
			break
		}

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

func sendJSONLine(conn net.Conn, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request payload: %w", err)
	}

	if _, err := conn.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write request payload: %w", err)
	}

	return nil
}

func readLine(prompt string, reader cliinput.Reader) (string, error) {
	line, err := reader.ReadLine(prompt)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(line), nil
}

func readPassword(prompt string, reader cliinput.Reader) (string, error) {
	line, err := reader.ReadPassword(prompt)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(line), nil
}

func promptNewUser(reader cliinput.Reader) (models.User, error) {
	uname, err := readLine("New Username: ", reader)
	if err != nil {
		return models.User{}, err
	}

	pass, err := readPassword("New Password: ", reader)
	if err != nil {
		return models.User{}, err
	}

	role, err := readLine("Role (admin/user): ", reader)
	if err != nil {
		return models.User{}, err
	}

	permissions := map[string]string{}
	if role != auth.RoleAdmin {
		fmt.Println("Enter table permissions (e.g., table1=read or table2=write). Leave blank to finish:")
		for {
			line, err := readLine("Permission: ", reader)
			if err != nil {
				return models.User{}, err
			}
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
	}, nil
}

func promptUpdateUserPermissions(reader cliinput.Reader, username string) (models.User, error) {
	permissions := map[string]string{}
	fmt.Println("Enter table permissions (e.g., table1=read or table2=write). Leave blank to finish:")
	for {
		line, err := readLine("Permission: ", reader)
		if err != nil {
			return models.User{}, err
		}
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
	}, nil
}

func promptUpdateUserPassword(reader cliinput.Reader, username string) (models.User, error) {
	pass, err := readPassword("New Password: ", reader)
	if err != nil {
		return models.User{}, err
	}

	return models.User{
		Username: username,
		Password: pass,
	}, nil
}

func promptUpdateUserRole(reader cliinput.Reader, username string) (models.User, error) {
	role, err := readLine("Role (admin/user): ", reader)
	if err != nil {
		return models.User{}, err
	}

	return models.User{
		Username: username,
		Role:     role,
	}, nil
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

	startupLogOffset := logFileSize(paths.logFile)
	logFile := openLogFile(paths.logFile)
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	err = cmd.Start()
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
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

	// Stream startup logs from the background process until it signals readiness.
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	if err := streamStartupLogsUntilReady(paths.logFile, startupLogOffset, done); err != nil {
		_ = os.Remove(paths.pidFile)
		log.Fatalf("Server failed to start: %v", err)
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

func logFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		return 0
	}
	return info.Size()
}

func streamStartupLogsUntilReady(logFilePath string, startOffset int64, done <-chan error) error {
	file, err := os.OpenFile(logFilePath, os.O_RDONLY|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("open startup log: %w", err)
	}
	defer file.Close()

	if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
		return fmt.Errorf("seek startup log: %w", err)
	}

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err == nil {
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				continue
			}
			if isServerReadyLogLine(line) {
				return nil
			}
			fmt.Println(line)
			continue
		}

		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("read startup log: %w", err)
		}

		select {
		case waitErr := <-done:
			if waitErr != nil {
				return waitErr
			}
			return errors.New("server exited before signaling readiness")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func isServerReadyLogLine(line string) bool {
	return strings.Contains(line, "ShibuDB server started on port ")
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

func rebuildIndex(paths runtimePaths, space string) {
	if running, pid := isServerRunning(paths.pidFile); running {
		fmt.Printf("%sError:%s ShibuDB server is running (PID: %d).\n", red, reset, pid)
		fmt.Println("Stop the server before rebuilding indexes to avoid concurrent file writes.")
		os.Exit(1)
	}

	result, err := spaces.RebuildSpaceIndex(paths.libDir, space)
	if err != nil {
		fmt.Printf("%sError:%s Failed to rebuild index for %q: %v\n", red, reset, space, err)
		os.Exit(1)
	}

	fmt.Printf("%s%s%s\n", green, result, reset)
}

type managerAuthConfig struct {
	username  string
	password  string
	authFile  string
	tokenFile string
}

func handleManagerCommand(managementPort string, args []string, authCfg managerAuthConfig) {
	if len(args) < 1 {
		fmt.Println("Usage: shibudb manager [--port <n>] [--data-dir <path>] [--username <u> --password <p>] <command> [args...]")
		printManagerUsage()
		return
	}

	command := args[0]
	baseURL := fmt.Sprintf("http://localhost:%s", managementPort)
	if command == "generate-token" || command == "list-tokens" || command == "delete-token" {
		handleTokenManagementCommand(command, args[1:], authCfg)
		return
	}

	adminUsername, err := ensureAdminCredentials(&authCfg)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	tokenMgr, err := auth.NewTokenManager(authCfg.tokenFile)
	if err != nil {
		fmt.Printf("Error: failed to initialize token manager: %v\n", err)
		return
	}
	tempTokenID, tempToken, err := tokenMgr.GenerateToken(adminUsername)
	if err != nil {
		fmt.Printf("Error: failed to generate management token: %v\n", err)
		return
	}
	defer func() {
		if err := tokenMgr.DeleteToken(tempTokenID); err != nil {
			fmt.Printf("Warning: failed to delete temporary token %s: %v\n", tempTokenID, err)
		}
	}()

	// Test connectivity first
	if !testManagementConnectivity(baseURL, tempToken) {
		fmt.Printf("Error: Cannot connect to management server at %s\n", baseURL)
		fmt.Printf("Please ensure the server is running and the management port is accessible.\n")
		return
	}

	switch command {
	case "status":
		getManagerStatus(baseURL, tempToken)
	case "stats":
		getManagerStats(baseURL, tempToken)
	case "update-space-settings":
		updateManagerSpaceSettings(baseURL, tempToken, args[1:])
	case "limit":
		if len(args) < 2 {
			fmt.Println("Usage: shibudb manager [--port <n>] [--username <u> --password <p>] limit <new_limit>")
			return
		}
		newLimit, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Printf("Error: Invalid limit value: %s\n", args[1])
			return
		}
		setManagerLimit(baseURL, tempToken, int32(newLimit))
	case "increase":
		amount := 100
		if len(args) >= 2 {
			if amt, err := strconv.Atoi(args[1]); err == nil {
				amount = amt
			}
		}
		increaseManagerLimit(baseURL, tempToken, int32(amount))
	case "decrease":
		amount := 100
		if len(args) >= 2 {
			if amt, err := strconv.Atoi(args[1]); err == nil {
				amount = amt
			}
		}
		decreaseManagerLimit(baseURL, tempToken, int32(amount))
	case "health":
		checkManagerHealth(baseURL, tempToken)
	case "reset":
		resetManagerLimit(baseURL, tempToken)
	default:
		fmt.Printf("Error: Unknown command: %s\n", command)
		printManagerUsage()
	}
}

func testManagementConnectivity(baseURL, bearerToken string) bool {
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

	req, err := http.NewRequest(http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		fmt.Printf("HTTP connectivity request creation failed: %v\n", err)
		return false
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	resp, err := client.Do(req)
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

func ensureAdminCredentials(cfg *managerAuthConfig) (string, error) {
	reader, err := cliinput.New(os.Stdin, os.Stdout)
	if err != nil {
		return "", fmt.Errorf("failed to initialize CLI input: %w", err)
	}
	defer reader.Close()

	if strings.TrimSpace(cfg.username) == "" {
		cfg.username, err = readLine("Admin Username: ", reader)
		if err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(cfg.password) == "" {
		cfg.password, err = readLine("Admin Password: ", reader)
		if err != nil {
			return "", err
		}
	}

	authManager, err := auth.NewAuthManager(cfg.authFile)
	if err != nil {
		return "", fmt.Errorf("failed to initialize auth manager: %w", err)
	}
	user, err := authManager.Authenticate(cfg.username, cfg.password)
	if err != nil {
		return "", fmt.Errorf("authentication failed: %w", err)
	}
	if user.Role != auth.RoleAdmin {
		return "", errors.New("admin access required")
	}
	return cfg.username, nil
}

func handleTokenManagementCommand(command string, args []string, authCfg managerAuthConfig) {
	adminUsername, err := ensureAdminCredentials(&authCfg)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	tokenMgr, err := auth.NewTokenManager(authCfg.tokenFile)
	if err != nil {
		fmt.Printf("Error: failed to initialize token manager: %v\n", err)
		return
	}

	switch command {
	case "generate-token":
		tokenID, rawToken, err := tokenMgr.GenerateToken(adminUsername)
		if err != nil {
			fmt.Printf("Error: failed to generate token: %v\n", err)
			return
		}
		fmt.Println("Token generated successfully.")
		fmt.Printf("Token ID: %s\n", tokenID)
		fmt.Printf("Token: %s\n", rawToken)
		fmt.Println("Store this token securely. It will not be shown again.")
	case "list-tokens":
		tokens := tokenMgr.ListTokens()
		if len(tokens) == 0 {
			fmt.Println("No management tokens found.")
			return
		}
		fmt.Println("Management Tokens:")
		for _, t := range tokens {
			fmt.Printf("- id=%s created_by=%s created_at=%s\n", t.ID, t.CreatedBy, t.CreatedAt.Format(time.RFC3339))
		}
	case "delete-token":
		if len(args) < 1 {
			fmt.Println("Usage: shibudb manager [--username <u> --password <p>] delete-token <token_id>")
			return
		}
		if err := tokenMgr.DeleteToken(strings.TrimSpace(args[0])); err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		fmt.Printf("Deleted token: %s\n", strings.TrimSpace(args[0]))
	default:
		fmt.Printf("Error: Unknown token command: %s\n", command)
	}
}

func printManagerUsage() {
	fmt.Printf(`Manager Commands:
  status                    Show current connection limit and active connections
  stats                     Show detailed connection statistics
  update-space-settings     [--segment-rollover-bytes N] [--max-segments-before-merge N] <space>
  limit <new_limit>         Set connection limit to specific value
  increase [amount]         Increase connection limit by amount (default: 100)
  decrease [amount]         Decrease connection limit by amount (default: 100)
  health                    Check server health
  reset                     Reset connection limit to configured default
  generate-token            Generate a new management bearer token
  list-tokens               List stored management tokens
  delete-token <token_id>   Delete a management token by id

Usage:
  shibudb manager [--port <management_port>] [--data-dir <path>] [--username <u> --password <p>] <command> [args...]
  Default --port is %s (must match the server’s --management-port).

Examples:
  shibudb manager --username admin --password admin status
  shibudb manager --username admin --password admin update-space-settings --segment-rollover-bytes 52428800 my_space
  shibudb manager --username admin --password admin limit 2000
  shibudb manager --username admin --password admin increase 500
  shibudb manager --username admin --password admin decrease 200
  shibudb manager --username admin --password admin reset
  shibudb manager --username admin --password admin stats
  shibudb manager --username admin --password admin generate-token
  shibudb manager --username admin --password admin list-tokens
  shibudb manager --username admin --password admin delete-token <token_id>
  shibudb manager --port 19090 limit 2000   # when server uses --management-port 19090
`, server.DefaultManagementPort)
}

func makeManagerRequest(method, url string, body interface{}, bearerToken string) (*http.Response, error) {
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
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	// Add timeout to prevent infinite wait
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	fmt.Printf("Making request to: %s %s\n", method, url)
	return client.Do(req)
}

func updateManagerSpaceSettings(baseURL, bearerToken string, args []string) {
	fs := flag.NewFlagSet("update-space-settings", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rolloverBytes := fs.Int64("segment-rollover-bytes", 0, "segment rollover threshold in bytes")
	maxSegments := fs.Int("max-segments-before-merge", 0, "max segments before background merge")
	usage := "Usage: shibudb manager [--port <n>] [--username <u> --password <p>] update-space-settings [--segment-rollover-bytes <bytes>] [--max-segments-before-merge <n>] <space>"
	if err := fs.Parse(args); err != nil {
		fmt.Printf("Error: %v\n", err)
		fmt.Println(usage)
		return
	}
	if len(fs.Args()) < 1 {
		fmt.Println(usage)
		return
	}
	if *rolloverBytes <= 0 && *maxSegments <= 0 {
		fmt.Println("Error: provide at least one of --segment-rollover-bytes or --max-segments-before-merge (flags must come before the space name)")
		fmt.Println(usage)
		return
	}

	body := map[string]interface{}{
		"space": fs.Args()[0],
	}
	if *rolloverBytes > 0 {
		body["segment_rollover_bytes"] = *rolloverBytes
	}
	if *maxSegments > 0 {
		body["max_segments_before_merge"] = *maxSegments
	}

	resp, err := makeManagerRequest(http.MethodPut, baseURL+"/spaces/settings", body, bearerToken)
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

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Error: %v\n", result["error"])
		return
	}

	fmt.Printf("Success: %v\n", result["message"])
	fmt.Printf("Space: %v\n", result["space"])
	fmt.Printf("Segment Rollover Bytes: %v\n", result["segment_rollover_bytes"])
	fmt.Printf("Max Segments Before Merge: %v\n", result["max_segments_before_merge"])
}

func getManagerStatus(baseURL, bearerToken string) {
	resp, err := makeManagerRequest("GET", baseURL+"/limit", nil, bearerToken)
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
	fmt.Printf("Busy Connections: %d\n", int(result["busy_connections"].(float64)))
	fmt.Printf("Idle Connections: %d\n", int(result["idle_connections"].(float64)))
	fmt.Printf("Available Slots: %d\n", int(result["available_slots"].(float64)))
}

func getManagerStats(baseURL, bearerToken string) {
	fmt.Printf("Connecting to management server at: %s\n", baseURL)

	resp, err := makeManagerRequest("GET", baseURL+"/stats", nil, bearerToken)
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
	fmt.Printf("Busy Connections: %d\n", int(result["busy_connections"].(float64)))
	fmt.Printf("Idle Connections: %d\n", int(result["idle_connections"].(float64)))
	fmt.Printf("Max Connections: %d\n", int(result["max_connections"].(float64)))
	fmt.Printf("Usage Percentage: %.1f%%\n", result["usage_percentage"].(float64))
	fmt.Printf("Available Slots: %d\n", int(result["available_slots"].(float64)))
}

func setManagerLimit(baseURL, bearerToken string, newLimit int32) {
	body := map[string]interface{}{
		"limit": newLimit,
	}

	resp, err := makeManagerRequest("PUT", baseURL+"/limit", body, bearerToken)
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

func increaseManagerLimit(baseURL, bearerToken string, amount int32) {
	body := map[string]interface{}{
		"amount": amount,
	}

	resp, err := makeManagerRequest("POST", baseURL+"/limit/increase", body, bearerToken)
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

func decreaseManagerLimit(baseURL, bearerToken string, amount int32) {
	body := map[string]interface{}{
		"amount": amount,
	}

	resp, err := makeManagerRequest("POST", baseURL+"/limit/decrease", body, bearerToken)
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

func checkManagerHealth(baseURL, bearerToken string) {
	resp, err := makeManagerRequest("GET", baseURL+"/health", nil, bearerToken)
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

func resetManagerLimit(baseURL, bearerToken string) {
	// Reset to configured default limit.
	defaultLimit := resolveDefaultMaxConnections()
	body := map[string]interface{}{
		"limit": defaultLimit,
	}

	resp, err := makeManagerRequest("PUT", baseURL+"/limit", body, bearerToken)
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
