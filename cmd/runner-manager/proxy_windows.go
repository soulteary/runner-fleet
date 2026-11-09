package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/soulteary/runner-fleet/internal/childenv"
	"github.com/soulteary/runner-fleet/internal/windowsrunner"
	"golang.org/x/sys/windows/registry"
)

// Capture from the installing user's HKCU, never the LocalSystem service's HKCU.
func importUserProxy(configPath string, replace bool) error {
	path := windowsrunner.ProxyFile(configPath)
	if !replace {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	settings := windowsrunner.ProxyFromEnvironment(childenv.Environ())
	if settings["http_proxy"] == "" && settings["https_proxy"] == "" {
		key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
		if err != nil && !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("read current user's Windows proxy settings: %w", err)
		}
		if err == nil {
			defer func() { _ = key.Close() }()
			enabled, _, err := key.GetIntegerValue("ProxyEnable")
			if err != nil && !errors.Is(err, registry.ErrNotExist) {
				return err
			}
			if enabled != 0 {
				server, _, err := key.GetStringValue("ProxyServer")
				if err != nil {
					return err
				}
				if server == "" {
					return fmt.Errorf("Windows system proxy is enabled but its address is empty")
				}
				bypass, _, err := key.GetStringValue("ProxyOverride")
				if err != nil && !errors.Is(err, registry.ErrNotExist) {
					return err
				}
				settings, err = windowsrunner.ParseWindowsProxy(server, bypass)
				if err != nil {
					return err
				}
			} else if pac, _, _ := key.GetStringValue("AutoConfigURL"); pac != "" {
				return fmt.Errorf("PAC proxy scripts cannot be imported; enable the proxy application's fixed HTTP/mixed system proxy or configure proxy.env manually")
			}
		}
	}
	if err := windowsrunner.ProtectManager(filepath.Dir(path)); err != nil {
		return err
	}
	if err := windowsrunner.WriteProxyFile(path, settings); err != nil {
		return err
	}
	fmt.Printf("Saved proxy settings to %s (proxy credentials are not printed).\n", path)
	return nil
}

func platformConfigureEnvironment(configPath string) error {
	settings, err := windowsrunner.ReadProxyFile(windowsrunner.ProxyFile(configPath))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, key := range []string{"http_proxy", "https_proxy", "no_proxy"} {
		if err := os.Setenv(key, settings[key]); err != nil {
			return err
		}
	}
	return nil
}
