package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSingleSpace(t *testing.T) {
	// Get the current working directory (project root)
	currentDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Error getting current directory: %v", err)
	}

	// Use absolute paths to prevent creating testdata at root
	configPath := filepath.Join(currentDir, "cmd/server/testdata/config.json")
	dataPath := filepath.Join(currentDir, "cmd/server/testdata/shibudb")

	StartServer("4444", configPath, 10, dataPath)
}
