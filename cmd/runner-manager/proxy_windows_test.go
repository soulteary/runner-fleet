package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/soulteary/runner-fleet/internal/windowsrunner"
	"golang.org/x/sys/windows"
)

func TestWindowsManagerLoadsCapturedProxyWithoutOtherEnvironment(t *testing.T) {
	for _, key := range []string{"http_proxy", "https_proxy", "no_proxy"} {
		t.Setenv(key, "http://old:8888")
	}
	t.Setenv("BASIC_AUTH_PASSWORD", "manager-only")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := windowsrunner.WriteProxyFile(windowsrunner.ProxyFile(configPath), map[string]string{"https_proxy": "http://127.0.0.1:7890"}); err != nil {
		t.Fatal(err)
	}
	if err := platformConfigureEnvironment(configPath); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("https_proxy") != "http://127.0.0.1:7890" || os.Getenv("http_proxy") != "" || os.Getenv("no_proxy") != "" || os.Getenv("BASIC_AUTH_PASSWORD") != "manager-only" {
		t.Fatal("captured proxy was not applied independently of other environment")
	}
	if err := windowsrunner.WriteProxyFile(windowsrunner.ProxyFile(configPath), nil); err != nil {
		t.Fatal(err)
	}
	if err := platformConfigureEnvironment(configPath); err != nil || os.Getenv("https_proxy") != "" {
		t.Fatal("disabling proxy left a stale environment", err)
	}
}

func TestWindowsProxyImportPreservesExistingSnapshotUntilExplicitRefresh(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("proxy snapshot ACLs require an administrator session")
	}
	t.Setenv("http_proxy", "http://127.0.0.1:7890")
	t.Setenv("https_proxy", "http://127.0.0.1:7890")
	t.Setenv("no_proxy", "localhost")
	configPath := filepath.Join(t.TempDir(), "data", "config.yaml")
	if err := importUserProxy(configPath, false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("https_proxy", "http://127.0.0.1:8888")
	if err := importUserProxy(configPath, false); err != nil {
		t.Fatal(err)
	}
	settings, err := windowsrunner.ReadProxyFile(windowsrunner.ProxyFile(configPath))
	if err != nil || settings["https_proxy"] != "http://127.0.0.1:7890" {
		t.Fatal("installation overwrote existing proxy settings", err)
	}
	if err := importUserProxy(configPath, true); err != nil {
		t.Fatal(err)
	}
	settings, err = windowsrunner.ReadProxyFile(windowsrunner.ProxyFile(configPath))
	if err != nil || settings["https_proxy"] != "http://127.0.0.1:8888" {
		t.Fatal("explicit import did not refresh proxy settings", err)
	}
}
