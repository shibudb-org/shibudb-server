package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shibudb.org/shibudb-server/internal/logger"
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

	logger.Infof("server", "Connection limit saved to: %s", cfgFile)
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

// LogLevelConfig stores the persisted log level.
type LogLevelConfig struct {
	Level       string `json:"level"`
	LastUpdated string `json:"last_updated"`
}

// SaveLogLevel persists the log level to disk under dataDir.
func SaveLogLevel(dataDir string, level logger.Level) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	cfgFile := filepath.Join(dataDir, "log_level.json")
	config := LogLevelConfig{
		Level:       level.String(),
		LastUpdated: fmt.Sprintf("%d", time.Now().Unix()),
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal log level config: %v", err)
	}

	if err := os.WriteFile(cfgFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write log level config: %v", err)
	}
	return nil
}

// LoadLogLevel loads the persisted log level from dataDir. If log_level.json
// is missing, it returns an error for which os.IsNotExist(err) is true.
func LoadLogLevel(dataDir string) (logger.Level, error) {
	cfgFile := filepath.Join(dataDir, "log_level.json")
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		if os.IsNotExist(err) {
			return logger.LevelInfo, err
		}
		return logger.LevelInfo, fmt.Errorf("failed to read log level config: %v", err)
	}

	var config LogLevelConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return logger.LevelInfo, fmt.Errorf("failed to parse log level config: %v", err)
	}

	return logger.ParseLevel(config.Level)
}

// GetPersistentLimit returns the limit to use, preferring the persisted value over defaultLimit.
func GetPersistentLimit(dataDir string, defaultLimit int32) int32 {
	persistedLimit, err := LoadConnectionLimit(dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultLimit
		}
		logger.Warnf("server", "Failed to load persisted limit: %v; using default limit: %d", err, defaultLimit)
		return defaultLimit
	}

	if persistedLimit > 0 {
		logger.Infof("server", "Loaded persisted connection limit: %d", persistedLimit)
		return persistedLimit
	}

	return defaultLimit
}
