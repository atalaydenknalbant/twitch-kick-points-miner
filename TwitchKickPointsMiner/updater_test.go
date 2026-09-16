package twitchchannelpointsminer

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  int
	}{
		{left: "v2.1.0", right: "2.0.9", want: 1},
		{left: "2.0", right: "2.0.0", want: 0},
		{left: "1.9.9", right: "2.0.0", want: -1},
		{left: "v2.0.1+build", right: "2.0.1", want: 0},
	}
	for _, test := range tests {
		if got := compareVersions(test.left, test.right); got != test.want {
			t.Fatalf("compareVersions(%q, %q) got %d want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestPickReleaseAssetRequiresGitHubDigest(t *testing.T) {
	name := updateAssetPrefix + "-windows-amd64.exe"
	hash := strings.Repeat("a", 64)
	assets := []releaseAsset{
		{Name: name, BrowserDownloadURL: "https://example.com/app", Size: 10, Digest: "sha256:" + hash},
	}
	binary, err := pickReleaseAsset(assets, "windows", "amd64")
	if err != nil {
		t.Fatalf("pickReleaseAsset returned error: %v", err)
	}
	if binary.Name != name || binary.Digest != "sha256:"+hash {
		t.Fatalf("unexpected asset: %#v", binary)
	}

	assets[0].Digest = ""
	if _, err := pickReleaseAsset(assets, "windows", "amd64"); err == nil {
		t.Fatal("missing GitHub digest should fail")
	}
}

func TestReleaseAssetChecksum(t *testing.T) {
	hash := strings.Repeat("a", 64)
	got, err := releaseAssetChecksum(releaseAsset{Name: "asset", Digest: "sha256:" + hash})
	if err != nil || got != hash {
		t.Fatalf("releaseAssetChecksum got %q, %v", got, err)
	}
	if _, err := releaseAssetChecksum(releaseAsset{Name: "asset", Digest: "sha512:" + hash}); err == nil {
		t.Fatal("wrong digest algorithm should fail")
	}
	if _, err := releaseAssetChecksum(releaseAsset{Name: "asset", Digest: "sha256:not-hex"}); err == nil {
		t.Fatal("invalid digest should fail")
	}
}

func TestHandleUpdateStartupCleanupPreservesUserArguments(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper")
	backup := filepath.Join(dir, "backup")
	for _, path := range []string{helper, backup} {
		if err := os.WriteFile(path, []byte("temporary"), 0o600); err != nil {
			t.Fatalf("write temporary file: %v", err)
		}
	}

	handled, remaining, err := HandleUpdateStartup([]string{updateCleanupFlag, helper, backup, updateArgsMarker, "-config", "settings.json"})
	if err != nil || handled {
		t.Fatalf("cleanup mode got handled=%t err=%v", handled, err)
	}
	if len(remaining) != 2 || remaining[0] != "-config" || remaining[1] != "settings.json" {
		t.Fatalf("remaining arguments got %#v", remaining)
	}
	for _, path := range []string{helper, backup} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cleanup did not remove %s", path)
		}
	}
}

func TestCopyExecutable(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	if runtime.GOOS == "windows" {
		source += ".exe"
		target += ".exe"
	}
	if err := os.WriteFile(source, []byte("new executable"), 0o755); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := copyExecutable(source, target); err != nil {
		t.Fatalf("copyExecutable returned error: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new executable" {
		t.Fatalf("copied content got %q, %v", data, err)
	}
}

func TestNewUpdateRequestRejectsPlainHTTP(t *testing.T) {
	if _, err := newUpdateRequest("http://example.com/update"); err == nil {
		t.Fatalf("plain HTTP update URL should fail")
	}
	if _, err := newUpdateRequest("https://example.com/update"); err != nil {
		t.Fatalf("HTTPS update URL should pass: %v", err)
	}
}

func TestDownloadVerifiedAsset(t *testing.T) {
	binaryData := []byte("verified release binary")
	hash := fmt.Sprintf("%x", sha256.Sum256(binaryData))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/binary":
			response.Header().Set("Content-Length", fmt.Sprint(len(binaryData)))
			_, _ = response.Write(binaryData)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	path, err := downloadVerifiedAsset(
		releaseAsset{Name: "asset.exe", BrowserDownloadURL: server.URL + "/binary", Size: int64(len(binaryData))},
		hash,
		t.TempDir(),
	)
	if err != nil {
		t.Fatalf("downloadVerifiedAsset returned error: %v", err)
	}
	defer os.Remove(path)
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(binaryData) {
		t.Fatalf("downloaded content got %q, %v", got, err)
	}

	if _, err := downloadVerifiedAsset(
		releaseAsset{Name: "asset.exe", BrowserDownloadURL: server.URL + "/binary", Size: int64(len(binaryData))},
		strings.Repeat("0", 64),
		t.TempDir(),
	); err == nil || !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Fatalf("mismatched GitHub digest should fail, got %v", err)
	}
}
