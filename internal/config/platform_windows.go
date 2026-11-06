package config

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

func defaultServerAddr() string        { return "127.0.0.1" }
func platformIdentity(s string) string { return strings.ToLower(s) }
func validatePlatformConfig(c *Config) error {
	if runtime.GOARCH != "amd64" || c.Runners.ContainerMode {
		return fmt.Errorf("Windows supports only x64 native runner services (container_mode=false)")
	}
	return nil
}
func safePlatformName(s string) bool {
	if strings.ContainsAny(s, `<>:"|?*`) || strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") {
		return false
	}
	for _, r := range s {
		if r < 32 {
			return false
		}
	}
	stem := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
		return false
	}
	return true
}
func resolvePlatformPaths(c *Config, configPath string) error {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(c.Runners.BasePath) {
		c.Runners.BasePath = filepath.Join(filepath.Dir(abs), c.Runners.BasePath)
	}
	return nil
}
