package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL, ListenAddress string
	LogLevel                   slog.Level
	WebTokenPath               string
	WebPublicRead              bool
	WebBasemap                 string
}

func Load() (Config, error) {
	c := Config{DatabaseURL: os.Getenv("AARDE_DATABASE_URL"), ListenAddress: os.Getenv("AARDE_LISTEN_ADDRESS")}
	if c.ListenAddress == "" {
		c.ListenAddress = ":8080"
	}
	level := os.Getenv("AARDE_LOG_LEVEL")
	if level == "" {
		level = "info"
	}
	if err := c.LogLevel.UnmarshalText([]byte(level)); err != nil {
		return c, fmt.Errorf("AARDE_LOG_LEVEL: %w", err)
	}
	if value := strings.TrimSpace(os.Getenv("AARDE_WEB_PUBLIC_READ")); value != "" {
		var err error
		c.WebPublicRead, err = strconv.ParseBool(value)
		if err != nil {
			return c, fmt.Errorf("AARDE_WEB_PUBLIC_READ must be a boolean (true or false), got %q", value)
		}
	}
	c.WebTokenPath = strings.TrimSpace(os.Getenv("AARDE_WEB_TOKEN_PATH"))
	c.WebBasemap = strings.TrimSpace(os.Getenv("AARDE_WEB_BASEMAP"))
	switch c.WebBasemap {
	case "":
		c.WebBasemap = "osm"
	case "osm", "none":
	default:
		return c, fmt.Errorf("AARDE_WEB_BASEMAP must be osm or none, got %q", c.WebBasemap)
	}
	return c, nil
}

// TokenPath is resolved only for serve; offline CLI commands need no home directory.
func (c Config) TokenPath() (string, error) {
	if c.WebTokenPath != "" {
		return filepath.Abs(c.WebTokenPath)
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.Getenv("XDG_STATE_HOME")
	}
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve web token path: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Abs(filepath.Join(base, "aarde", "web.token"))
}
