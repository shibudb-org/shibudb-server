package server

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const (
	configDir  = "/usr/local/var/lib/shibudb"
	configFile = "/usr/local/var/lib/shibudb/connection_limit.json"
)

// ConnectionConfig stores persistent connection settings
type ConnectionConfig struct {
	MaxConnections int32  `json:"max_connections"`
	LastUpdated    string `json:"last_updated"`
}

// SaveConnectionLimit persists the connection limit to disk
func SaveConnectionLimit(limit int32) error {
	// Ensure config directory exists
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	config := ConnectionConfig{
		MaxConnections: limit,
		LastUpdated:    fmt.Sprintf("%d", time.Now().Unix()),
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	if err := os.WriteFile(configFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %v", err)
	}

	fmt.Printf("Connection limit saved to: %s\n", configFile)
	return nil
}

// LoadConnectionLimit loads the persisted connection limit
func LoadConnectionLimit() (int32, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, return default
			return 1000, nil
		}
		return 0, fmt.Errorf("failed to read config file: %v", err)
	}

	var config ConnectionConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return 0, fmt.Errorf("failed to parse config file: %v", err)
	}

	return config.MaxConnections, nil
}

// GetPersistentLimit returns the limit to use, preferring persisted value over default
func GetPersistentLimit(defaultLimit int32) int32 {
	persistedLimit, err := LoadConnectionLimit()
	if err != nil {
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
