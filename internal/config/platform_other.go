//go:build !windows

package config

func defaultServerAddr() string                  { return "0.0.0.0" }
func platformIdentity(s string) string           { return s }
func safePlatformName(string) bool               { return true }
func validatePlatformConfig(*Config) error       { return nil }
func resolvePlatformPaths(*Config, string) error { return nil }
