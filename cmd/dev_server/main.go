package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	// Get the current directory (should be project root)
	currentDir, err := os.Getwd()
	if err != nil {
		fmt.Printf("Error getting current directory: %v\n", err)
		os.Exit(1)
	}

	// Environment variables will be set per-command below

	// Verify include path exists
	includePath := filepath.Join(currentDir, "resources", "lib", "include")
	if _, err := os.Stat(includePath); os.IsNotExist(err) {
		fmt.Printf("❌ Error: Include path does not exist: %s\n", includePath)
		os.Exit(1)
	}

	// Verify FAISS C API header exists
	faissHeaderPath := filepath.Join(includePath, "faiss", "c_api", "AutoTune_c.h")
	if _, err := os.Stat(faissHeaderPath); os.IsNotExist(err) {
		fmt.Printf("❌ Error: FAISS header does not exist: %s\n", faissHeaderPath)
		os.Exit(1)
	}

	fmt.Printf("✅ Include path verified: %s\n", includePath)
	fmt.Printf("✅ FAISS header verified: %s\n", faissHeaderPath)

	// Create config file with admin user data in the correct location
	configDir := filepath.Join(currentDir, "cmd/server/testdata")
	configFile := filepath.Join(configDir, "config.json")

	// Ensure testdata directory exists
	if err := os.MkdirAll(configDir, 0755); err != nil {
		fmt.Printf("Error creating config directory: %v\n", err)
		os.Exit(1)
	}

	// Create config file with admin user data
	configData := `{
  "admin": {
    "username": "admin",
    "password": "$2a$10$Ag11HDzTDQmQp7QOP6cPk.EZtogMEI868tSz90Y.WHqgyTmYHDDbu",
    "role": "admin",
    "permissions": {}
  }
}`

	if err := os.WriteFile(configFile, []byte(configData), 0644); err != nil {
		fmt.Printf("Error creating config file: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("🔧 Created config file with admin user data")

	// Create a temporary directory for the test binary
	tempDir, err := os.MkdirTemp("", "dev_server_debug")
	if err != nil {
		fmt.Printf("Error creating temp directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tempDir)

	testBinary := filepath.Join(tempDir, "dev_server_test")

	fmt.Println("🔧 Building test binary...")

	// Build the test binary with environment variables
	buildCmd := exec.Command("go", "test", "-c", "./cmd/server", "-run", "TestSingleSpace", "-o", testBinary)
	buildCmd.Dir = currentDir
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr

	// Set environment variables for the command
	buildCmd.Env = append(os.Environ(),
		"CGO_ENABLED=1",
		fmt.Sprintf("CGO_CFLAGS=-I%s/resources/lib/include", currentDir),
		fmt.Sprintf("CGO_CXXFLAGS=-I%s/resources/lib/include", currentDir),
		fmt.Sprintf("CPATH=%s/resources/lib/include", currentDir),
	)

	// Add platform-specific library flags
	if runtime.GOOS == "darwin" {
		buildCmd.Env = append(buildCmd.Env,
			fmt.Sprintf("CGO_LDFLAGS=-L%s/resources/lib/mac/apple_silicon -lfaiss -lfaiss_c -lc++", currentDir))
	} else if runtime.GOOS == "linux" {
		// Detect architecture for Linux
		arch := runtime.GOARCH
		libDir := "amd64"
		if arch == "arm64" {
			libDir = "arm64"
		}
		buildCmd.Env = append(buildCmd.Env,
			fmt.Sprintf("CGO_LDFLAGS=-L%s/resources/lib/linux/%s -lfaiss -lfaiss_c -lstdc++ -lm -lgomp -lopenblas", currentDir, libDir))
	}

	// Environment variables are set correctly for cross-platform compatibility

	if err := buildCmd.Run(); err != nil {
		fmt.Printf("Error building test binary: %v\n", err)
		os.Exit(1)
	}

	// Add RPATH to the test binary (platform-specific)
	if runtime.GOOS == "darwin" {
		fmt.Println("🔧 Adding RPATH to test binary (macOS)...")
		rpathCmd := exec.Command("install_name_tool", "-add_rpath", "/usr/local/lib", testBinary)
		if err := rpathCmd.Run(); err != nil {
			fmt.Printf("Error adding RPATH: %v\n", err)
			os.Exit(1)
		}
	} else if runtime.GOOS == "linux" {
		fmt.Println("🔧 Setting up library path for Linux...")
		// On Linux, we'll use LD_LIBRARY_PATH instead of RPATH
		os.Setenv("LD_LIBRARY_PATH", "/usr/local/lib:"+os.Getenv("LD_LIBRARY_PATH"))
	}

	fmt.Println("🚀 Running dev server test...")

	// Run the test binary with environment variables
	runCmd := exec.Command(testBinary, "-test.v")
	runCmd.Dir = currentDir
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr

	// Set environment variables for the command (including LD_LIBRARY_PATH for Linux)
	runCmd.Env = append(os.Environ())

	if runtime.GOOS == "linux" {
		// Add library path for Linux
		ldPath := "/usr/local/lib"
		if existingPath := os.Getenv("LD_LIBRARY_PATH"); existingPath != "" {
			ldPath = ldPath + ":" + existingPath
		}
		runCmd.Env = append(runCmd.Env, fmt.Sprintf("LD_LIBRARY_PATH=%s", ldPath))
	}

	if err := runCmd.Run(); err != nil {
		fmt.Printf("Error running test: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ Dev server test completed!")
}
