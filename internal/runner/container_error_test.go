package runner

import (
	"errors"
	"strings"
	"testing"
)

func TestDockerCmdError_EmptyOutput(t *testing.T) {
	err := dockerCmdError("docker create", []byte(""), errors.New("exit status 1"))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "docker create 失败") || !strings.Contains(msg, "(无输出)") {
		t.Fatalf("unexpected error message: %s", msg)
	}
}

func TestDockerCmdError_PermissionHint(t *testing.T) {
	out := []byte("permission denied while trying to connect to the Docker daemon socket")
	err := dockerCmdError("docker start", out, errors.New("exit status 1"))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "权限不足或无法连接 daemon") {
		t.Fatalf("expected daemon permission hint, got: %s", msg)
	}
	if !strings.Contains(msg, "DOCKER_GID") {
		t.Fatalf("expected docker access hint, got: %s", msg)
	}
}
