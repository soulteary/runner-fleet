//go:build windows || windowsrunner_portable

package windowsrunner

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipPackage(t *testing.T, extra string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range []string{"config.cmd", "bin/Runner.Listener.exe", "bin/RunnerService.exe", "externals/node/bin/node.exe", extra} {
		if name == "" {
			continue
		}
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("test executable")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func releaseServer(t *testing.T, archive []byte, digest, body string) (*httptest.Server, *int) {
	t.Helper()
	downloads := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest", "/releases/tags/vtest":
			if r.Header.Get("User-Agent") != "runner-fleet" {
				t.Error("missing API User-Agent")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "vtest", "body": body, "assets": []map[string]string{{"name": "actions-runner-win-x64-test.zip", "browser_download_url": server.URL + "/runner.zip", "digest": digest}}})
		case "/runner.zip":
			downloads++
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &downloads
}

func TestInstallerInstallsVerifiedPackageAndReusesInstallation(t *testing.T) {
	archive := zipPackage(t, "")
	hash := sha256.Sum256(archive)
	server, downloads := releaseServer(t, archive, "sha256:"+hex.EncodeToString(hash[:]), "")
	dir := filepath.Join(t.TempDir(), "构建 tools", "runner")
	i := Installer{APIBase: server.URL, Version: "test"}
	if err := i.Install(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if !Complete(dir) {
		t.Fatal("incomplete installation")
	}
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("identity"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := i.Install(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if *downloads != 1 {
		t.Fatalf("downloaded an existing installation %d times", *downloads)
	}
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil || len(entries) != 1 {
		t.Fatalf("staging files leaked: %v %v", entries, err)
	}
}

func TestInstallerRejectsChecksumAndUnsafeArchivesBeforePromotion(t *testing.T) {
	for _, name := range []string{"wrong-digest", "../escaped", `bin\escaped`, "bin/evil:stream", "/absolute", ".credentials", "bin/trailing.", "bin/../escape", "bin//empty", "CONFIG.CMD", "bin/CON", "bin/aux.txt", "bin/LPT1", "bin/bad?name", "bin/control\x01"} {
		t.Run(name, func(t *testing.T) {
			extra := name
			if name == "wrong-digest" {
				extra = ""
			}
			archive := zipPackage(t, extra)
			hash := sha256.Sum256(archive)
			digest := hex.EncodeToString(hash[:])
			if name == "wrong-digest" {
				digest = strings.Repeat("0", 64)
			}
			server, _ := releaseServer(t, archive, "sha256:"+digest, "")
			dir := filepath.Join(t.TempDir(), "runner")
			if err := (Installer{APIBase: server.URL}).Install(context.Background(), dir); err == nil {
				t.Fatal("accepted unsafe installation")
			}
			if Complete(dir) {
				t.Fatal("promoted unsafe installation")
			}
			if _, err := os.Stat(filepath.Join(dir, "config.cmd")); !os.IsNotExist(err) {
				t.Fatal("wrote package files before validation")
			}
		})
	}
}

func TestInstallerUsesOfficialBodyHashAndRejectsMissingHash(t *testing.T) {
	archive := zipPackage(t, "")
	hash := sha256.Sum256(archive)
	for _, body := range []string{"", fmt.Sprintf("%x  actions-runner-win-x64-test.zip", hash), fmt.Sprintf("<!-- BEGIN SHA win-x64 -->%x<!-- END SHA win-x64 -->", hash)} {
		server, downloads := releaseServer(t, archive, "", body)
		err := (Installer{APIBase: server.URL}).Install(context.Background(), filepath.Join(t.TempDir(), "runner"))
		if body == "" && (err == nil || *downloads != 0) {
			t.Fatal("missing official hash did not fail closed")
		}
		if body != "" && err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstallerPreservesRegisteredIncompleteRunnerAndHandlesHTTPFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("registered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (Installer{}).Install(context.Background(), dir); err == nil {
		t.Fatal("overwrote incomplete registration")
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	if err := (Installer{APIBase: server.URL}).Install(context.Background(), t.TempDir()); err == nil {
		t.Fatal("accepted HTTP failure")
	}
	if err := (Installer{Version: "../bad"}).Install(context.Background(), t.TempDir()); err == nil {
		t.Fatal("accepted invalid version")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Installer{APIBase: server.URL}).Install(ctx, t.TempDir()); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestInstallerRejectsSymlinkAndIncompleteArchive(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: "bin/link"}
	h.SetMode(os.ModeSymlink | 0777)
	f, err := w.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("../../escape"))
	_ = w.Close()
	for _, archive := range [][]byte{buf.Bytes(), []byte("not a ZIP")} {
		hash := sha256.Sum256(archive)
		server, _ := releaseServer(t, archive, fmt.Sprintf("sha256:%x", hash), "")
		if err := (Installer{APIBase: server.URL}).Install(context.Background(), t.TempDir()); err == nil {
			t.Fatal("accepted invalid package")
		}
	}
}

func TestInstallerResumesInterruptedPromotion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runner")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.cmd", "bin/Runner.Listener.exe", "bin/RunnerService.exe", ".fleet-installing"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if Complete(dir) {
		t.Fatal("interrupted promotion reported complete")
	}
	archive := zipPackage(t, "")
	hash := sha256.Sum256(archive)
	server, _ := releaseServer(t, archive, fmt.Sprintf("sha256:%x", hash), "")
	if err := (Installer{APIBase: server.URL}).Install(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if !Complete(dir) {
		t.Fatal("interrupted installation did not recover")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "externals", "node", "bin", "node.exe")); err != nil || string(data) != "test executable" {
		t.Fatal("incomplete package promotion", err)
	}
}

func TestInstallerReportsMalformedReleaseMissingAssetAndDownloadFailure(t *testing.T) {
	for _, body := range []string{"not JSON", `{"tag_name":"vtest","assets":[]}`, `{"tag_name":"vtest","assets":[{"name":"actions-runner-win-x64-test.zip","browser_download_url":"/broken","digest":"sha256:bad"}]}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		err := (Installer{APIBase: server.URL}).Install(context.Background(), t.TempDir())
		server.Close()
		if err == nil {
			t.Fatal("accepted unusable release metadata")
		}
	}
}
