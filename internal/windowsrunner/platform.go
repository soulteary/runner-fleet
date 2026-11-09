// Package windowsrunner provides the Windows native runner backend.
// Pure installer/controller tests can also run on Unix with -tags windowsrunner_portable.
package windowsrunner

import "runtime"

const Enabled = runtime.GOOS == "windows"
