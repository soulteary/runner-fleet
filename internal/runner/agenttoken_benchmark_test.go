package runner

import (
	"errors"
	"testing"
)

// The failure is already reported; repeated status polling must keep deduplication.
func BenchmarkWarnAgentTokenUnavailableRepeated(b *testing.B) {
	dir := b.TempDir()
	err := errors.New("fixture credential storage failure")
	WarnAgentTokenUnavailable(dir, err)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		WarnAgentTokenUnavailable(dir, err)
	}
}
