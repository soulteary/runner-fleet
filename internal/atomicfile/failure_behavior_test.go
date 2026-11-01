package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileMissingParentDoesNotCreatePartialTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "state.json")
	if err := WriteFile(path, []byte("complete"), 0600); err == nil {
		t.Fatal("missing parent accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial target exists: %v", err)
	}
	syncDir(filepath.Dir(path))
}

func TestWriteTempReportsActualWriteAndFlushFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	readOnly, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTemp(readOnly, []byte("replacement"), 0600); err == nil || !strings.Contains(err.Error(), "cannot write") {
		t.Fatalf("write failure: %v", err)
	}
	if got := mustRead(t, path); got != "original" {
		t.Fatal("read-only record changed")
	}
	device, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTemp(device, []byte("replacement"), 0600); err == nil || !strings.Contains(err.Error(), "cannot flush") {
		t.Fatalf("flush failure: %v", err)
	}
	if _, err := device.Write([]byte("x")); err == nil {
		t.Fatal("failed flush left descriptor open")
	}
}
