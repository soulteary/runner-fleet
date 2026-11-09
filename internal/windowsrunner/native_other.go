//go:build !windows && windowsrunner_portable

package windowsrunner

import "fmt"

func openNative() (Manager, error) {
	return nil, fmt.Errorf("Windows service control is unavailable on this platform")
}
func ProtectManager(dir string) error    { return nil }
func ProtectRunner(dir string) error     { return nil }
func removeRunnerGroup(dir string) error { return nil }
