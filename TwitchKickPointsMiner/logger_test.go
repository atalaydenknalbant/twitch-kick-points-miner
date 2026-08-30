package twitchchannelpointsminer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

func TestSanitizeFilename(t *testing.T) {
	name := `bad/name\\:*?"<>|`
	got := sanitizeFilename(name)
	if got == name || got == "" {
		t.Fatalf("sanitize did not change invalid name: %q", got)
	}
	if strings.ContainsAny(got, `/\\:*?"<>|`) {
		t.Fatalf("sanitized name still has forbidden chars: %q", got)
	}
}

func TestNewLoggerCreatesFileWhenSaveEnabled(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd error: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(wd)

	logger := NewLogger(LoggerSettings{Save: true}, "tester")
	logger.Printf("hello")
	logger.Printf("%s and %s", constants.PlatformTwitchToken, constants.PlatformKickToken)

	logPath := filepath.Join("log", "tester.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected log file at %s: %v", logPath, err)
	}
	if w, ok := logger.base.Writer().(dualWriter); ok {
		if f, ok := w.file.(*os.File); ok {
			_ = f.Close()
		}
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	text := string(content)
	if strings.Contains(text, "TKPM_PLATFORM") || !strings.Contains(text, "Twitch and Kick") {
		t.Fatalf("saved platform labels got %q", text)
	}
}

func TestEmojize(t *testing.T) {
	if got := emojize(":rocket:"); got == ":rocket:" {
		t.Fatalf("expected known emoji mapping, got %q", got)
	}
	if got := emojize(":unknown:"); got != ":unknown:" {
		t.Fatalf("unknown emoji should pass through, got %q", got)
	}
}

func TestFormatBannerLine(t *testing.T) {
	if got := formatBannerLine("hello", 10); got != "| hello      |" {
		t.Fatalf("banner line got %q", got)
	}
	if got := formatBannerLine("123456", 4); got != "| 1234 |" {
		t.Fatalf("truncated banner line got %q", got)
	}
}

func TestPlatformTokensUsePlainNamesOutsideConsole(t *testing.T) {
	message := constants.PlatformTwitchToken + " channel | " + constants.PlatformKickToken + " channel"
	if got := plainPlatformTokens(message); got != "Twitch channel | Kick channel" {
		t.Fatalf("plain platform message got %q", got)
	}
}

func TestTerminalPlatformWriterUsesUnicodeFallback(t *testing.T) {
	t.Setenv("TKPM_PLATFORM_LOGOS", "unicode")
	var output bytes.Buffer
	writer := terminalPlatformWriter{output: &output}
	message := constants.PlatformTwitchToken + " | " + constants.PlatformKickToken
	if _, err := writer.Write([]byte(message)); err != nil {
		t.Fatalf("write platform logos: %v", err)
	}
	got := output.String()
	if strings.Contains(got, "TKPM_PLATFORM") || !strings.Contains(got, "T") || !strings.Contains(got, "K") {
		t.Fatalf("unicode platform output got %q", got)
	}
}

func TestEmbeddedPlatformLogosProduceSixel(t *testing.T) {
	twitch := buildTerminalLogoSixel(twitchLogoPNG, false)
	kick := buildTerminalLogoSixel(kickLogoPNG, true)
	for name, logo := range map[string]string{"twitch": twitch, "kick": kick} {
		if !strings.HasPrefix(logo, "\x1bP") || !strings.HasSuffix(logo, "\x1b\\") {
			t.Fatalf("%s sixel framing is invalid", name)
		}
		if len(logo) < 50 {
			t.Fatalf("%s sixel output is unexpectedly short: %d", name, len(logo))
		}
	}
}

func TestFormatBannerLineCountsPlatformTokenAsLogo(t *testing.T) {
	got := formatBannerLine(constants.PlatformTwitchToken+" followed", 12)
	if !strings.Contains(got, constants.PlatformTwitchToken) || !strings.HasSuffix(got, " |") {
		t.Fatalf("platform banner line got %q", got)
	}
}
