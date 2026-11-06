//go:build windows || windowsrunner_portable

package windowsrunner

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/soulteary/runner-fleet/internal/atomicfile"
	"github.com/soulteary/runner-fleet/internal/childenv"
)

var proxyKeys = []string{"http_proxy", "https_proxy", "no_proxy"}

const proxyBegin = "# BEGIN Runner Fleet managed proxy"
const proxyEnd = "# END Runner Fleet managed proxy"

// ProxyFromEnvironment copies only proxy settings, never Manager credentials.
func ProxyFromEnvironment(environ []string) map[string]string {
	out := map[string]string{}
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.ToLower(key)
		if ok && isProxyKey(key) && value != "" {
			out[key] = value
		}
	}
	return out
}

func isProxyKey(key string) bool {
	for _, candidate := range proxyKeys {
		if key == candidate {
			return true
		}
	}
	return false
}

// ParseWindowsProxy converts the manual Internet Settings format used by Clash
// and v2ray into runner environment variables. PAC scripts are not evaluated.
func ParseWindowsProxy(server, bypass string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.Split(server, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		protocol, address, perProtocol := strings.Cut(part, "=")
		protocol, address = strings.ToLower(strings.TrimSpace(protocol)), strings.TrimSpace(address)
		if !perProtocol {
			address = part
		} else if protocol != "http" && protocol != "https" {
			continue
		}
		if !strings.Contains(address, "://") {
			address = "http://" + address
		}
		u, err := url.Parse(address)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || strings.ContainsAny(address, "\r\n") {
			return nil, fmt.Errorf("Windows system proxy is not a valid HTTP/HTTPS proxy address")
		}
		if perProtocol {
			out[protocol+"_proxy"] = address
		} else {
			out["http_proxy"], out["https_proxy"] = address, address
		}
	}
	if len(out) == 0 && strings.TrimSpace(server) != "" {
		return nil, fmt.Errorf("Windows system proxy has no HTTP/HTTPS endpoint; select the proxy application's HTTP or mixed port")
	}
	if len(out) != 0 {
		// Keep local service traffic direct. WinINET wildcard rules cannot all be
		// represented by no_proxy; import only literal hosts and domain suffixes.
		hosts := []string{"localhost", "127.0.0.1", "::1"}
		for _, host := range strings.Split(bypass, ";") {
			host = strings.TrimSpace(host)
			if host == "" || host == "<local>" {
				continue
			}
			host = strings.TrimPrefix(host, "*.")
			if !strings.ContainsAny(host, "*?<>\r\n") {
				hosts = append(hosts, host)
			}
		}
		out["no_proxy"] = strings.Join(hosts, ",")
	}
	return out, nil
}

func ProxyFile(configPath string) string { return filepath.Join(filepath.Dir(configPath), "proxy.env") }

func ReadProxyFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if !ok || !isProxyKey(key) {
			return nil, fmt.Errorf("proxy.env line %d must set http_proxy, https_proxy or no_proxy", i+1)
		}
		out[key] = strings.TrimSpace(value)
	}
	return out, nil
}

func proxyLines(settings map[string]string, skip map[string]bool) (string, error) {
	var b strings.Builder
	for _, key := range proxyKeys {
		value := settings[key]
		if skip[key] || value == "" {
			continue
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return "", fmt.Errorf("invalid newline or NUL in proxy setting %s", key)
		}
		fmt.Fprintf(&b, "%s=%s\n", key, value)
	}
	return b.String(), nil
}

func WriteProxyFile(path string, settings map[string]string) error {
	content, err := proxyLines(settings, nil)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, []byte(content), 0600)
}

// SyncRunnerProxy updates only our marked block. User-written proxy entries
// outside it override inherited values, and unrelated runner settings survive.
func SyncRunnerProxy(dir string) error {
	return syncRunnerProxy(dir, ProxyFromEnvironment(childenv.Environ()))
}

func syncRunnerProxy(dir string, settings map[string]string) error {
	path := filepath.Join(dir, ".env")
	original, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var kept []string
	skip := map[string]bool{}
	inside := false
	for _, line := range strings.Split(strings.ReplaceAll(string(original), "\r\n", "\n"), "\n") {
		switch line {
		case proxyBegin:
			if inside {
				return fmt.Errorf("runner .env contains nested managed proxy blocks")
			}
			inside = true
			continue
		case proxyEnd:
			if !inside {
				return fmt.Errorf("runner .env contains an unmatched managed proxy marker")
			}
			inside = false
			continue
		}
		if inside {
			continue
		}
		kept = append(kept, line)
		key, _, ok := strings.Cut(line, "=")
		if ok && isProxyKey(strings.ToLower(strings.TrimSpace(key))) {
			skip[strings.ToLower(strings.TrimSpace(key))] = true
		}
	}
	if inside {
		return fmt.Errorf("runner .env contains an unterminated managed proxy block")
	}
	block, err := proxyLines(settings, skip)
	if err != nil {
		return err
	}
	content := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if content != "" {
		content += "\n"
	}
	if block != "" {
		content += proxyBegin + "\n" + block + proxyEnd + "\n"
	}
	if string(original) == content || len(original) == 0 && content == "" {
		return nil
	}
	return atomicfile.WriteFile(path, []byte(content), 0600)
}
