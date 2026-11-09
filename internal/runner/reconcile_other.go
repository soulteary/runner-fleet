//go:build !windows

package runner

import "context"

func ReconcileWindows(context.Context, string) {}
