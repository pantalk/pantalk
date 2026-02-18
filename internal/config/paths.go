package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultConfigPath returns the resolved config file path using a fallback
// chain:
//
//  1. $PANTALK_CONFIG environment variable (if set and non-empty)
//  2. $XDG_CONFIG_HOME/pantalk/config.yaml (if XDG_CONFIG_HOME is set)
//  3. ~/.config/pantalk/config.yaml
func DefaultConfigPath() string {
	if envPath := strings.TrimSpace(os.Getenv("PANTALK_CONFIG")); envPath != "" {
		return envPath
	}

	return filepath.Join(xdgConfigHome(), "pantalk", "config.yaml")
}

// DefaultSocketPath returns the resolved socket path using a fallback chain:
//
//  1. $XDG_RUNTIME_DIR/pantalk.sock (if XDG_RUNTIME_DIR is set)
//  2. /tmp/pantalk-<uid>.sock (per-user fallback)
func DefaultSocketPath() string {
	if runtimeDir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); runtimeDir != "" {
		return filepath.Join(runtimeDir, "pantalk.sock")
	}

	return fmt.Sprintf("/tmp/pantalk-%d.sock", os.Getuid())
}

// DefaultDBPath returns the resolved database path using a fallback chain:
//
//  1. $XDG_DATA_HOME/pantalk/pantalk.db (if XDG_DATA_HOME is set)
//  2. ~/.local/share/pantalk/pantalk.db
func DefaultDBPath() string {
	return filepath.Join(xdgDataHome(), "pantalk", "pantalk.db")
}

// DefaultSkillsCachePath returns the resolved cache directory for the skills
// repository clone using a fallback chain:
//
//  1. $XDG_CACHE_HOME/pantalk/skills (if XDG_CACHE_HOME is set)
//  2. ~/.cache/pantalk/skills
func DefaultSkillsCachePath() string {
	return filepath.Join(xdgCacheHome(), "pantalk", "skills")
}

// EnsureDir creates all parent directories for the given file path if they do
// not already exist. This is used to prepare config, data, and socket
// directories at startup.
func EnsureDir(filePath string) error {
	dir := filepath.Dir(filePath)
	return os.MkdirAll(dir, 0o700)
}

func xdgConfigHome() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); dir != "" {
		return dir
	}
	return filepath.Join(homeDir(), ".config")
}

func xdgCacheHome() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); dir != "" {
		return dir
	}
	return filepath.Join(homeDir(), ".cache")
}

func xdgDataHome() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dir != "" {
		return dir
	}
	return filepath.Join(homeDir(), ".local", "share")
}

func homeDir() string {
	if home := os.Getenv("HOME"); home != "" {
		return home
	}

	// fallback for unusual environments
	return "/tmp/pantalk-" + strconv.Itoa(os.Getuid())
}
