package config

import (
	"fmt"
	"github.com/GerhardOfRivia/aarde/internal/raster"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Viewer                     raster.ViewerOptions
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
	case "osm", "offline", "none":
	default:
		return c, fmt.Errorf("AARDE_WEB_BASEMAP must be osm, offline, or none, got %q", c.WebBasemap)
	}
	c.Viewer = raster.DefaultViewerOptions()
	for key, dst := range map[string]*int{"AARDE_VIEWER_MAX_DIMENSION": &c.Viewer.MaxDimension, "AARDE_VIEWER_CONCURRENT": &c.Viewer.Concurrent} {
		if value := os.Getenv(key); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				return c, fmt.Errorf("%s must be an integer", key)
			}
			*dst = n
		}
	}
	for key, dst := range map[string]*int64{"AARDE_VIEWER_LAYER_PIXELS": &c.Viewer.LayerPixels, "AARDE_VIEWER_SCENE_PIXELS": &c.Viewer.ScenePixels} {
		if value := os.Getenv(key); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return c, fmt.Errorf("%s must be an integer", key)
			}
			*dst = n
		}
	}
	if value := os.Getenv("AARDE_VIEWER_TIMEOUT"); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil {
			return c, fmt.Errorf("AARDE_VIEWER_TIMEOUT: %w", err)
		}
		c.Viewer.Timeout = d
	}
	c.Viewer.TempDir = os.Getenv("AARDE_VIEWER_TEMP_DIR")
	if value := os.Getenv("AARDE_VIEWER_SOURCE_ROOTS"); value != "" {
		for _, root := range filepath.SplitList(value) {
			if !filepath.IsAbs(root) || filepath.Clean(root) != root {
				return c, fmt.Errorf("AARDE_VIEWER_SOURCE_ROOTS requires absolute clean directories")
			}
			c.Viewer.SourceRoots = append(c.Viewer.SourceRoots, root)
		}
	}
	if err := c.Viewer.Validate(); err != nil {
		return c, err
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
