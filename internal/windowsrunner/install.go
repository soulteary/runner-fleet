//go:build windows || windowsrunner_portable

// Package windowsrunner installs and controls the official Windows Actions runner.
// The installer and lifecycle orchestration are portable so they can be tested
// without Windows; only the SCM and ACL adapters require Windows.
package windowsrunner

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const maxArchiveBytes int64 = 1 << 30
const maxExpandedBytes uint64 = 8 << 30

type Installer struct {
	Client  *http.Client
	APIBase string
	Version string
}

func Complete(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".fleet-installing")); err == nil || !os.IsNotExist(err) {
		return false
	}
	for _, name := range []string{"config.cmd", "bin/Runner.Listener.exe", "bin/RunnerService.exe"} {
		if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
			return false
		}
	}
	return true
}

// Install verifies the entire archive before writing any package files into dir.
// Existing registrations are never overwritten, including partially configured ones.
func (i Installer) Install(ctx context.Context, dir string) error {
	if Complete(dir) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".runner")); err == nil {
		return fmt.Errorf("registered runner has an incomplete installation; restore its files before retrying")
	} else if !os.IsNotExist(err) {
		return err
	}
	client := i.Client
	if client == nil {
		client = http.DefaultClient
	}
	api := strings.TrimRight(i.APIBase, "/")
	if api == "" {
		api = "https://api.github.com/repos/actions/runner"
	}
	endpoint := api + "/releases/latest"
	if i.Version != "" {
		version := strings.TrimPrefix(i.Version, "v")
		if strings.ContainsAny(version, "/\\?#") {
			return fmt.Errorf("invalid runner version")
		}
		endpoint = api + "/releases/tags/v" + version
	}
	resp, err := get(ctx, client, endpoint)
	if err != nil {
		return err
	}
	var release struct {
		Tag    string `json:"tag_name"`
		Body   string `json:"body"`
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&release)
	_ = resp.Body.Close()
	if err != nil {
		return fmt.Errorf("read official runner release: %w", err)
	}
	name := "actions-runner-win-x64-" + strings.TrimPrefix(release.Tag, "v") + ".zip"
	var url, digest string
	for _, a := range release.Assets {
		if a.Name == name {
			url, digest = a.URL, strings.TrimPrefix(a.Digest, "sha256:")
			break
		}
	}
	if url == "" {
		return fmt.Errorf("official release has no %s", name)
	}
	if digest == "" {
		for _, line := range strings.Split(release.Body, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
				digest = fields[0]
				break
			}
		}
		// Older official releases put platform hashes between HTML markers.
		if digest == "" {
			marker := regexp.MustCompile(`(?i)<!--\s*BEGIN SHA win-x64\s*-->\s*([a-f0-9]{64})\s*<!--\s*END SHA win-x64\s*-->`)
			if match := marker.FindStringSubmatch(release.Body); match != nil {
				digest = match[1]
			}
		}
	}
	expected, err := hex.DecodeString(digest)
	if err != nil || len(expected) != sha256.Size {
		return fmt.Errorf("official SHA256 is unavailable for %s", name)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".runner-install-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	archive := filepath.Join(stage, "runner.zip")
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	resp, err = get(ctx, client, url)
	if err != nil {
		_ = f.Close()
		return err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, maxArchiveBytes+1))
	_ = resp.Body.Close()
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxArchiveBytes {
		return fmt.Errorf("runner archive exceeds size limit")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), digest) {
		return fmt.Errorf("runner archive SHA256 mismatch")
	}
	root := filepath.Join(stage, "files")
	if err := extract(ctx, archive, root); err != nil {
		return err
	}
	if !Complete(root) {
		return fmt.Errorf("runner archive is missing required Windows executables")
	}
	marker := filepath.Join(dir, ".fleet-installing")
	if err := os.WriteFile(marker, []byte("installation in progress"), 0600); err != nil {
		return err
	}
	err = filepath.WalkDir(root, func(src string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, src)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, rel)
		if entry.IsDir() {
			return os.MkdirAll(dst, 0700)
		}
		// Only package files are replaced; configuration and credentials are not in the ZIP.
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return err
		}
		return os.Rename(src, dst)
	})
	if err != nil {
		return err
	}
	return os.Remove(marker)
}

func get(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "runner-fleet")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("runner download returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func extract(ctx context.Context, archive, root string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer func() { _ = z.Close() }()
	var total uint64
	seen := make(map[string]bool)
	if len(z.File) > 50000 {
		return fmt.Errorf("runner ZIP has too many entries")
	}
	for _, file := range z.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimSuffix(file.Name, "/")
		parts := strings.Split(name, "/")
		if name == "" || path.IsAbs(name) || strings.ContainsAny(name, "\\:\x00") || file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe ZIP entry %q", file.Name)
		}
		for _, p := range parts {
			if !safeZIPComponent(p) {
				return fmt.Errorf("unsafe ZIP path %q", file.Name)
			}
		}
		if strings.HasPrefix(parts[0], ".") {
			return fmt.Errorf("runner ZIP contains a reserved configuration path")
		}
		key := strings.ToLower(name)
		if seen[key] {
			return fmt.Errorf("duplicate ZIP entry %q", name)
		}
		seen[key] = true
		if file.UncompressedSize64 > maxExpandedBytes-total {
			return fmt.Errorf("expanded runner ZIP exceeds size limit")
		}
		total += file.UncompressedSize64
		dst := filepath.Join(root, filepath.FromSlash(name))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		if err := extractFile(file, dst); err != nil {
			return err
		}
	}
	return nil
}

func safeZIPComponent(p string) bool {
	if p == "" || p == "." || p == ".." || strings.HasSuffix(p, ".") || strings.HasSuffix(p, " ") || strings.ContainsAny(p, `<>"|?*`) {
		return false
	}
	for _, r := range p {
		if r < 32 {
			return false
		}
	}
	stem := strings.ToUpper(strings.SplitN(p, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" {
		return false
	}
	return !(len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9')
}

func extractFile(file *zip.File, dst string) error {
	r, err := file.Open()
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	w, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	closeErr := w.Close()
	if err != nil {
		return err
	}
	return closeErr
}
