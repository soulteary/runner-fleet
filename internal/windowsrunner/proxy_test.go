//go:build windows || windowsrunner_portable

package windowsrunner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsManualProxyFormats(t *testing.T) {
	for _, server := range []string{"127.0.0.1:7890", "http://127.0.0.1:7890", "http=127.0.0.1:7890;https=127.0.0.1:7890;socks=127.0.0.1:7891"} {
		settings, err := ParseWindowsProxy(server, "<local>;*.internal;build.local;192.168.*")
		if err != nil {
			t.Fatal(err)
		}
		if settings["http_proxy"] != "http://127.0.0.1:7890" || settings["https_proxy"] != settings["http_proxy"] {
			t.Fatalf("wrong manual proxy conversion: %#v", settings)
		}
		if settings["no_proxy"] != "localhost,127.0.0.1,::1,internal,build.local" {
			t.Fatal(settings["no_proxy"])
		}
	}
	for _, server := range []string{"http://", "socks=127.0.0.1:7891", "127.0.0.1:7890\nAGENT_TOKEN=secret"} {
		if _, err := ParseWindowsProxy(server, ""); err == nil {
			t.Fatalf("accepted unsupported/invalid proxy %q", server)
		}
	}
}

func TestWindowsRunnerProxyPreservesManualOverridesAndRefreshesManagedBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	manual := "# custom runner settings\r\nEXTRA=value=with=equals\r\nHTTPS_PROXY=http://manual:8080\r\n"
	if err := os.WriteFile(path, []byte(manual), 0600); err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{"http_proxy": "http://127.0.0.1:7890", "https_proxy": "http://inherited:7890", "no_proxy": "localhost"}
	if err := syncRunnerProxy(dir, settings); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first := read()
	if !strings.Contains(first, "HTTPS_PROXY=http://manual:8080") || strings.Contains(first, "inherited:7890") || !strings.Contains(first, "EXTRA=value=with=equals") {
		t.Fatal("manual overrides or other settings were overwritten")
	}
	if err := syncRunnerProxy(dir, settings); err != nil || read() != first {
		t.Fatalf("proxy sync is not idempotent: %v", err)
	}
	settings["http_proxy"] = "http://127.0.0.1:8888"
	if err := syncRunnerProxy(dir, settings); err != nil {
		t.Fatal(err)
	}
	if got := read(); strings.Contains(got, ":7890") || strings.Count(got, proxyBegin) != 1 || !strings.Contains(got, ":8888") {
		t.Fatal("managed proxy was not refreshed")
	}
	if err := syncRunnerProxy(dir, nil); err != nil {
		t.Fatal(err)
	}
	if got := read(); strings.Contains(got, proxyBegin) || !strings.Contains(got, "HTTPS_PROXY=http://manual:8080") {
		t.Fatal("disabling inherited proxy removed manual settings")
	}
}

func TestWindowsProxyFileRoundTripAndRejectsInjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.env")
	settings := ProxyFromEnvironment([]string{"HTTPS_PROXY=http://user:pass@localhost:7890", "http_proxy=http://localhost:7890", "NO_PROXY=localhost", "BASIC_AUTH_PASSWORD=secret", "AGENT_TOKEN=secret"})
	if len(settings) != 3 {
		t.Fatal("non-proxy environment was captured")
	}
	if err := WriteProxyFile(path, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadProxyFile(path)
	if err != nil || loaded["https_proxy"] != settings["https_proxy"] {
		t.Fatal("proxy credentials did not round trip", err)
	}
	settings["http_proxy"] = "http://localhost\nAGENT_TOKEN=secret"
	if err := WriteProxyFile(path, settings); err == nil {
		t.Fatal("accepted environment injection")
	}
	if err := os.WriteFile(path, []byte("BASIC_AUTH_PASSWORD=secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadProxyFile(path); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("invalid proxy file was accepted or leaked its contents")
	}
}

func TestWindowsRunnerProxyRejectsMalformedBlockWithoutTruncating(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	for _, content := range []string{proxyBegin + "\nKEEP=1\n", proxyEnd + "\nKEEP=1\n", proxyBegin + "\n" + proxyBegin + "\n" + proxyEnd} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := syncRunnerProxy(dir, map[string]string{"http_proxy": "http://localhost:7890"}); err == nil {
			t.Fatal("accepted malformed managed proxy block")
		}
		b, _ := os.ReadFile(path)
		if string(b) != content {
			t.Fatal("damaged user .env on error")
		}
	}
}
