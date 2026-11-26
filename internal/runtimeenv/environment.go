// Package runtimeenv contains process configuration shared by both entry modes.
package runtimeenv

import (
	"os"
	"path/filepath"
)

func Value(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func SocketPath(configPath string) string {
	return Value("PANEL_SOCKET", filepath.Join(filepath.Dir(configPath), ".miproxy", "panel.sock"))
}
