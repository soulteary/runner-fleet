package runner

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
)

func TestWarnAgentTokenUnavailableConcurrentOnce(t *testing.T) {
	var output bytes.Buffer
	original := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(original) })
	dir := t.TempDir()
	err := errors.New("fixture storage unavailable")
	WarnAgentTokenUnavailable(dir, nil)
	if output.Len() != 0 {
		t.Fatal("nil error logged")
	}
	var group sync.WaitGroup
	for range 64 {
		group.Go(func() { WarnAgentTokenUnavailable(dir, err) })
	}
	group.Wait()
	if got := strings.Count(output.String(), "fixture storage unavailable"); got != 1 {
		t.Fatalf("warning count=%d, want one", got)
	}
	WarnAgentTokenUnavailable(dir, err)
	if strings.Count(output.String(), "fixture storage unavailable") != 1 {
		t.Fatal("cached warning repeated")
	}
}
