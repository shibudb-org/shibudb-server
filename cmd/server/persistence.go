package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ConnectionConfig stores persistent connection settings
type ConnectionConfig struct {
	MaxConnections int32  `json:"max_connections"`
	LastUpdated    string `json:"last_updated"`
}

// SaveConnectionLimit persists the connection limit to disk under dataDir.
func SaveConnectionLimit(dataDir string, limit int32) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	cfgFile := filepath.Join(dataDir, "connection_limit.json")
	config := ConnectionConfig{
		MaxConnections: limit,
		LastUpdated:    fmt.Sprintf("%d", time.Now().Unix()),
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	if err := os.WriteFile(cfgFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %v", err)
	}

	fmt.Printf("Connection limit saved to: %s\n", cfgFile)
	return nil
}

// LoadConnectionLimit loads the persisted connection limit from dataDir.
// If connection_limit.json is missing, it returns an error for which os.IsNotExist(err) is true
// (so callers can distinguish “no persisted config” from a real on-disk limit).
func LoadConnectionLimit(dataDir string) (int32, error) {
	cfgFile := filepath.Join(dataDir, "connection_limit.json")
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, err
		}
		return 0, fmt.Errorf("failed to read config file: %v", err)
	}

	var config ConnectionConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return 0, fmt.Errorf("failed to parse config file: %v", err)
	}

	return config.MaxConnections, nil
}

// GetPersistentLimit returns the limit to use, preferring the persisted value over defaultLimit.
func GetPersistentLimit(dataDir string, defaultLimit int32) int32 {
	persistedLimit, err := LoadConnectionLimit(dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultLimit
		}
		fmt.Printf("Warning: Failed to load persisted limit: %v\n", err)
		fmt.Printf("Using default limit: %d\n", defaultLimit)
		return defaultLimit
	}

	if persistedLimit > 0 {
		fmt.Printf("Loaded persisted connection limit: %d\n", persistedLimit)
		return persistedLimit
	}

	return defaultLimit
}
