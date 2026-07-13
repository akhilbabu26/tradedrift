package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// GetEnv retrieves the environment variable or returns the default value if it is not set or is only whitespace.
// Unlike the other helpers, this never fails — absence just means default.
func GetEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return defaultValue
}

// GetEnvOrError retrieves the environment variable or returns an error if it is missing, empty, or only whitespace.
func GetEnvOrError(key string) (string, error) {
	value, exists := os.LookupEnv(key)
	if !exists {
		return "", fmt.Errorf("critical configuration variable %s is not set", key)
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("critical configuration variable %s is empty", key)
	}
	return trimmed, nil
}

// GetEnvAsInt parses an environment variable as an integer.
// It returns the defaultValue if the variable is absent or only whitespace, or an error if present but malformed.
func GetEnvAsInt(key string, defaultValue int) (int, error) {
	valueStr, exists := os.LookupEnv(key)
	if !exists {
		return defaultValue, nil
	}
	trimmed := strings.TrimSpace(valueStr)
	if trimmed == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid integer value for %s: %q: %w", key, trimmed, err)
	}
	return value, nil
}

// GetEnvAsDuration parses an environment variable as a time.Duration.
// It returns the defaultValue if the variable is absent or only whitespace, or an error if present but malformed.
func GetEnvAsDuration(key string, defaultValue time.Duration) (time.Duration, error) {
	valueStr, exists := os.LookupEnv(key)
	if !exists {
		return defaultValue, nil
	}
	trimmed := strings.TrimSpace(valueStr)
	if trimmed == "" {
		return defaultValue, nil
	}
	duration, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid time duration value for %s: %q: %w", key, trimmed, err)
	}
	return duration, nil
}

// ValidateLogLevel checks if the given string is a valid Zap log level.
// Note: This expects an already-defaulted value (e.g. from GetEnv), not a raw environment
// variable lookup. If the level is an empty string, it will return an error.
func ValidateLogLevel(level string) error {
	trimmed := strings.TrimSpace(level)
	switch trimmed {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("invalid LOG_LEVEL: %q (must be debug, info, warn, or error)", level)
	}
}
