package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const appDirName = "abx"

func defaultConfigPath() (string, error) {
	dir, err := xdgConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appDirName, "config.toml"), nil
}

func defaultSignalCLIDataDir() (string, error) {
	dir, err := xdgDataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "signal-cli"), nil
}

func defaultAuditFilePath() (string, error) {
	dir, err := xdgStateHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appDirName, "audit.log"), nil
}

func defaultDatabaseDSN() (string, error) {
	dir, err := xdgStateHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appDirName, "app.db"), nil
}

func defaultWorkDir() (string, error) {
	dir, err := xdgStateHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appDirName, "workspace"), nil
}

func xdgConfigHome() (string, error) {
	return xdgHome("XDG_CONFIG_HOME", ".config")
}

func xdgDataHome() (string, error) {
	return xdgHome("XDG_DATA_HOME", filepath.Join(".local", "share"))
}

func xdgStateHome() (string, error) {
	return xdgHome("XDG_STATE_HOME", filepath.Join(".local", "state"))
}

func xdgHome(envKey, fallback string) (string, error) {
	value := strings.TrimSpace(os.Getenv(envKey))
	if value != "" {
		if !filepath.IsAbs(value) {
			return "", fmt.Errorf("%s must be an absolute path", envKey)
		}
		return filepath.Clean(value), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, fallback), nil
}
