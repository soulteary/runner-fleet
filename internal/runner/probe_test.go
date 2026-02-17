package runner

import (
	"errors"
	"testing"
)

func TestDetectProbeErrorType_Wrapped(t *testing.T) {
	err := newProbeError(ProbeErrorTypeAgentHTTP, errors.New("agent 返回 502: bad gateway"))
	got := DetectProbeErrorType(err)
	if got != ProbeErrorTypeAgentHTTP {
		t.Fatalf("expected %s, got %s", ProbeErrorTypeAgentHTTP, got)
	}
}

func TestDetectProbeErrorType_FallbackByMessage(t *testing.T) {
	got := DetectProbeErrorType(errors.New("cannot connect to the Docker daemon"))
	if got != ProbeErrorTypeDockerAccess {
		t.Fatalf("expected %s, got %s", ProbeErrorTypeDockerAccess, got)
	}

	got = DetectProbeErrorType(errors.New("dial tcp: lookup runner-x: no such host"))
	if got != ProbeErrorTypeAgentConnect {
		t.Fatalf("expected %s, got %s", ProbeErrorTypeAgentConnect, got)
	}
}

func TestProbeSuggestion(t *testing.T) {
	if ProbeSuggestion(ProbeErrorTypeDockerAccess) == "" {
		t.Fatal("docker-access suggestion should not be empty")
	}
	if ProbeSuggestion(ProbeErrorTypeUnknown) == "" {
		t.Fatal("unknown suggestion should not be empty")
	}
}

func TestProbeCommands(t *testing.T) {
	if ProbeCheckCommand(ProbeErrorTypeDockerAccess) == "" {
		t.Fatal("docker-access check command should not be empty")
	}
	if ProbeFixCommand(ProbeErrorTypeDockerAccess) == "" {
		t.Fatal("docker-access fix command should not be empty")
	}
	if ProbeCheckCommand(ProbeErrorTypeUnknown) == "" || ProbeFixCommand(ProbeErrorTypeUnknown) == "" {
		t.Fatal("unknown commands should not be empty")
	}
}
