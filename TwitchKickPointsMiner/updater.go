package twitchchannelpointsminer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

const (
	updateApplyFlag   = "--tkpm-apply-update"
	updateCleanupFlag = "--tkpm-cleanup-update"
	updateArgsMarker  = "--"
	updateAssetPrefix = "TwitchKickPointsMiner"
	maxUpdateSize     = 200 << 20
)

var errNoPublishedRelease = errors.New("no published release")

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type githubRelease struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

func RunAutoUpdate() (bool, error) {
	exePath, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("locate executable: %w", err)
	}
	if isGoRunExecutable(exePath) {
		return false, nil
	}

	release, err := fetchLatestRelease()
	if errors.Is(err, errNoPublishedRelease) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if compareVersions(release.TagName, constants.Version) <= 0 {
		return false, nil
	}

	binary, checksum, err := pickReleaseAssets(release.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return false, err
	}

	log.Printf("update: version %s is available", release.TagName)
	tempPath, err := downloadVerifiedAsset(binary, checksum, filepath.Dir(exePath))
	if err != nil {
		return false, fmt.Errorf("download update: %w", err)
	}
	if err := launchUpdateHelper(exePath, tempPath, os.Args[1:]); err != nil {
		_ = os.Remove(tempPath)
		return false, fmt.Errorf("launch update helper: %w", err)
	}
	return true, nil
}

// HandleUpdateStartup processes private updater arguments before normal flag parsing.
func HandleUpdateStartup(args []string) (handled bool, remaining []string, err error) {
	if len(args) == 0 {
		return false, args, nil
	}

	switch args[0] {
	case updateApplyFlag:
		if len(args) < 3 || args[2] != updateArgsMarker {
			return true, nil, errors.New("invalid update helper arguments")
		}
		return true, nil, applyDownloadedUpdate(args[1], args[3:])
	case updateCleanupFlag:
		if len(args) < 4 || args[3] != updateArgsMarker {
			return true, nil, errors.New("invalid update cleanup arguments")
		}
		cleanupUpdateArtifacts(args[1], args[2])
		return false, args[4:], nil
	default:
		return false, args, nil
	}
}

func fetchLatestRelease() (githubRelease, error) {
	req, err := http.NewRequest(http.MethodGet, constants.ReleasesAPIURL, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", updateAssetPrefix+"-Updater")

	resp, err := newHTTPClient(15 * time.Second).Do(req)
	if err != nil {
		return githubRelease{}, fmt.Errorf("fetch release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return githubRelease{}, errNoPublishedRelease
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return githubRelease{}, fmt.Errorf("fetch release: unexpected status %s", resp.Status)
	}

	var release githubRelease
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	if err := decoder.Decode(&release); err != nil {
		return githubRelease{}, fmt.Errorf("parse release: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return githubRelease{}, errors.New("release has no tag")
	}
	return release, nil
}

func pickReleaseAssets(assets []releaseAsset, goos, arch string) (releaseAsset, releaseAsset, error) {
	name := fmt.Sprintf("%s-%s-%s", updateAssetPrefix, goos, arch)
	if goos == "windows" {
		name += ".exe"
	}

	var binary, checksum releaseAsset
	for _, asset := range assets {
		switch {
		case strings.EqualFold(asset.Name, name):
			binary = asset
		case strings.EqualFold(asset.Name, name+".sha256"):
			checksum = asset
		case strings.EqualFold(asset.Name, "checksums.txt") && checksum.Name == "":
			checksum = asset
		}
	}
	if binary.Name == "" {
		return releaseAsset{}, releaseAsset{}, fmt.Errorf("no release asset for %s/%s", goos, arch)
	}
	if checksum.Name == "" {
		return releaseAsset{}, releaseAsset{}, fmt.Errorf("release asset %s has no SHA256 checksum", binary.Name)
	}
	if binary.Size > maxUpdateSize {
		return releaseAsset{}, releaseAsset{}, fmt.Errorf("release asset %s is too large", binary.Name)
	}
	return binary, checksum, nil
}

func downloadVerifiedAsset(binary, checksum releaseAsset, dir string) (string, error) {
	expected, err := fetchExpectedChecksum(checksum.BrowserDownloadURL, binary.Name)
	if err != nil {
		return "", err
	}

	req, err := newUpdateRequest(binary.BrowserDownloadURL)
	if err != nil {
		return "", err
	}
	resp, err := newHTTPClient(5 * time.Minute).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("download asset: unexpected status %s", resp.Status)
	}
	if resp.ContentLength > maxUpdateSize {
		return "", fmt.Errorf("download asset exceeds %d bytes", maxUpdateSize)
	}

	pattern := ".tkpm-update-*"
	if runtime.GOOS == "windows" {
		pattern += ".exe"
	}
	temp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	keep := false
	defer func() {
		_ = temp.Close()
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()

	hasher := sha256.New()
	reader := io.Reader(resp.Body)
	progress := newProgressWriter(resp.ContentLength)
	if resp.ContentLength > 0 {
		log.Printf("update: downloading %.1f MB", bytesToMB(resp.ContentLength))
		reader = io.TeeReader(resp.Body, progress)
	}
	written, err := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(reader, maxUpdateSize+1))
	if err != nil {
		return "", err
	}
	if written > maxUpdateSize {
		return "", fmt.Errorf("download asset exceeds %d bytes", maxUpdateSize)
	}
	progress.Done()
	if err := temp.Sync(); err != nil {
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return "", fmt.Errorf("SHA256 mismatch for %s", binary.Name)
	}
	if err := os.Chmod(tempPath, 0o755); err != nil && !errors.Is(err, os.ErrPermission) {
		return "", fmt.Errorf("make update executable: %w", err)
	}
	keep = true
	return tempPath, nil
}

func fetchExpectedChecksum(rawURL, assetName string) (string, error) {
	req, err := newUpdateRequest(rawURL)
	if err != nil {
		return "", err
	}
	resp, err := newHTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("download checksum: unexpected status %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return parseExpectedChecksum(string(data), assetName)
}

func parseExpectedChecksum(data, assetName string) (string, error) {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		if len(fields) > 1 && !strings.EqualFold(strings.TrimPrefix(fields[len(fields)-1], "*"), assetName) {
			continue
		}
		if len(fields[0]) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err == nil {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksum for %s not found", assetName)
}

func newUpdateRequest(rawURL string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(req.URL.Scheme, "https") && req.URL.Hostname() != "127.0.0.1" && req.URL.Hostname() != "localhost" {
		return nil, errors.New("update URL must use HTTPS")
	}
	req.Header.Set("User-Agent", updateAssetPrefix+"-Updater")
	return req, nil
}

func launchUpdateHelper(targetPath, helperPath string, args []string) error {
	updateArgs := []string{updateApplyFlag, targetPath, updateArgsMarker}
	updateArgs = append(updateArgs, args...)
	cmd := exec.Command(helperPath, updateArgs...)
	cmd.Dir = filepath.Dir(targetPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

func applyDownloadedUpdate(targetPath string, relaunchArgs []string) error {
	helperPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate update helper: %w", err)
	}
	helperPath, err = filepath.Abs(helperPath)
	if err != nil {
		return err
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	if !samePath(filepath.Dir(helperPath), filepath.Dir(targetPath)) || samePath(helperPath, targetPath) {
		return errors.New("update helper and target paths are invalid")
	}

	backupPath := targetPath + ".previous"
	_ = os.Remove(backupPath)
	deadline := time.Now().Add(2 * time.Minute)
	for {
		err = os.Rename(targetPath, backupPath)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for running executable: %w", err)
		}
		time.Sleep(250 * time.Millisecond)
	}

	if err := copyExecutable(helperPath, targetPath); err != nil {
		_ = os.Rename(backupPath, targetPath)
		return err
	}
	cleanupArgs := []string{updateCleanupFlag, helperPath, backupPath, updateArgsMarker}
	cleanupArgs = append(cleanupArgs, relaunchArgs...)
	cmd := exec.Command(targetPath, cleanupArgs...)
	cmd.Dir = filepath.Dir(targetPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = os.Remove(targetPath)
		_ = os.Rename(backupPath, targetPath)
		return fmt.Errorf("restart updated executable: %w", err)
	}
	return nil
}

func copyExecutable(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open update helper: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("create updated executable: %w", err)
	}
	keep := false
	defer func() {
		_ = out.Close()
		if !keep {
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy updated executable: %w", err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync updated executable: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close updated executable: %w", err)
	}
	keep = true
	return nil
}

func cleanupUpdateArtifacts(paths ...string) {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			err := os.Remove(path)
			if err == nil || errors.Is(err, os.ErrNotExist) {
				break
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func normalizeVersion(raw string) string {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "v"))
	if idx := strings.IndexAny(raw, "-+"); idx >= 0 {
		raw = raw[:idx]
	}
	return raw
}

func compareVersions(a, b string) int {
	parse := func(raw string) []int {
		parts := strings.Split(normalizeVersion(raw), ".")
		values := make([]int, len(parts))
		for i, part := range parts {
			values[i], _ = strconv.Atoi(part)
		}
		return values
	}
	left, right := parse(a), parse(b)
	length := len(left)
	if len(right) > length {
		length = len(right)
	}
	for i := 0; i < length; i++ {
		var leftValue, rightValue int
		if i < len(left) {
			leftValue = left[i]
		}
		if i < len(right) {
			rightValue = right[i]
		}
		if leftValue > rightValue {
			return 1
		}
		if leftValue < rightValue {
			return -1
		}
	}
	return 0
}

func samePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func isGoRunExecutable(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, "go-build") || strings.HasPrefix(lower, strings.ToLower(os.TempDir()))
}

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

type progressWriter struct {
	total       int64
	written     int64
	lastPercent int
}

func newProgressWriter(total int64) *progressWriter {
	return &progressWriter{total: total, lastPercent: -5}
}

func (p *progressWriter) Write(data []byte) (int, error) {
	p.written += int64(len(data))
	p.logProgress(false)
	return len(data), nil
}

func (p *progressWriter) Done() {
	p.logProgress(true)
}

func (p *progressWriter) logProgress(done bool) {
	if p.total <= 0 {
		return
	}
	percent := int(float64(p.written) * 100 / float64(p.total))
	if percent > 100 || done {
		percent = 100
	}
	step := percent / 5 * 5
	if step <= p.lastPercent {
		return
	}
	p.lastPercent = step
	log.Printf("update: download %d%% (%.1f/%.1f MB)", step, bytesToMB(p.written), bytesToMB(p.total))
}

func bytesToMB(value int64) float64 {
	return float64(value) / (1024 * 1024)
}
