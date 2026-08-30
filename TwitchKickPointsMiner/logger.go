package twitchchannelpointsminer

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

type LoggerSettings struct {
	Save             bool            `json:"save"`
	ConsoleLevel     int             `json:"console_level"`
	FileLevel        int             `json:"file_level"`
	Emoji            bool            `json:"emoji"`
	Smart            bool            `json:"smart"`
	ShowSeconds      bool            `json:"show_seconds"`
	ConsoleUsername  bool            `json:"console_username"`
	ShowClaimedBonus bool            `json:"show_claimed_bonus_msg"`
	Less             bool            `json:"less"`
	Debug            bool            `json:"debug"`
	DebugDeep        bool            `json:"debug_deep"`
	AnonymizeLogs    bool            `json:"anonymize_logs"`
	Discord          DiscordSettings `json:"discord"`
}

type Logger struct {
	base     *log.Logger
	settings LoggerSettings
	discord  *DiscordWebhook
}

type dualWriter struct {
	console io.Writer
	file    io.Writer
}

func (w dualWriter) Write(p []byte) (int, error) {
	if w.console != nil {
		if _, err := w.console.Write(p); err != nil {
			return 0, err
		}
	}
	if w.file != nil {
		plain := []byte(plainPlatformTokens(string(p)))
		clean := ansiRegexp.ReplaceAll(plain, nil)
		if _, err := w.file.Write(clean); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

var ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func NewLogger(settings LoggerSettings, username string) *Logger {
	consoleOutput := terminalPlatformWriter{output: os.Stdout}
	var output io.Writer = consoleOutput
	if settings.Save {
		logDir := "log"
		if err := os.MkdirAll(logDir, 0o755); err == nil {
			name := strings.TrimSpace(username)
			if settings.AnonymizeLogs || name == "" {
				name = "miner"
			}
			name = sanitizeFilename(name)
			logPath := filepath.Join(logDir, fmt.Sprintf("%s.log", name))
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err == nil {
				output = dualWriter{
					console: consoleOutput,
					file:    f,
				}
			}
		}
	}
	return &Logger{
		base:     log.New(output, "", 0),
		settings: settings,
		discord:  NewDiscordWebhook(settings.Discord),
	}
}

func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	return replacer.Replace(name)
}

func (l *Logger) log(level, emoji, format string, args ...interface{}) {
	l.logEvent(level, emoji, "", format, args...)
}

func (l *Logger) logEvent(level, emoji string, event constants.Event, format string, args ...interface{}) {
	if level == "DEBUG" && !l.settings.Debug {
		return
	}
	if level == "DEEP" && (!l.settings.Debug || !l.settings.DebugDeep) {
		return
	}
	message := fmt.Sprintf(format, args...)
	if emoji != "" && l.settings.Emoji {
		message = fmt.Sprintf("%s %s", emojize(emoji), message)
	}
	if event != "" && l.discord != nil {
		clean := ansiRegexp.ReplaceAllString(plainPlatformTokens(message), "")
		l.discord.Send(clean, event)
	}
	timestampFormat := "15:04 02/01/06"
	if l.settings.ShowSeconds {
		timestampFormat = "15:04:05 02/01/06"
	}
	timestamp := time.Now().Format(timestampFormat)
	l.base.Printf("[%s] %s: %s", level, timestamp, message)
}

func (l *Logger) Printf(format string, args ...interface{}) {
	l.log("INFO", "", format, args...)
}

func (l *Logger) Println(v ...interface{}) {
	l.log("INFO", "", "%s", fmt.Sprint(v...))
}

func (l *Logger) Banner(lines ...string) {
	if l == nil || l.base == nil {
		return
	}
	const width = 58
	border := "+" + strings.Repeat("-", width+2) + "+"
	l.base.Println()
	l.base.Println(border)
	for _, line := range lines {
		l.base.Println(formatBannerLine(line, width))
	}
	l.base.Println(border)
	l.base.Println()
}

func formatBannerLine(line string, width int) string {
	line = strings.TrimSpace(line)
	if width < 1 {
		return "||"
	}

	var rendered strings.Builder
	columns := 0
	for len(line) > 0 && columns < width {
		if strings.HasPrefix(line, constants.PlatformTwitchToken) {
			if columns+2 > width {
				break
			}
			rendered.WriteString(constants.PlatformTwitchToken)
			line = line[len(constants.PlatformTwitchToken):]
			columns += 2
			continue
		}
		if strings.HasPrefix(line, constants.PlatformKickToken) {
			if columns+2 > width {
				break
			}
			rendered.WriteString(constants.PlatformKickToken)
			line = line[len(constants.PlatformKickToken):]
			columns += 2
			continue
		}
		r, size := utf8.DecodeRuneInString(line)
		if r == utf8.RuneError && size == 0 {
			break
		}
		rendered.WriteRune(r)
		line = line[size:]
		columns++
	}
	return "| " + rendered.String() + strings.Repeat(" ", width-columns) + " |"
}

func (l *Logger) Errorf(format string, args ...interface{}) {
	l.log("ERROR", "", format, args...)
}

func (l *Logger) Fatalf(format string, args ...interface{}) {
	l.log("ERROR", "", format, args...)
	os.Exit(1)
}

func (l *Logger) EmojiPrintf(emoji, format string, args ...interface{}) {
	l.log("INFO", emoji, format, args...)
}

func (l *Logger) Eventf(event constants.Event, format string, args ...interface{}) {
	l.logEvent("INFO", "", event, format, args...)
}

func (l *Logger) EmojiEventf(emoji string, event constants.Event, format string, args ...interface{}) {
	l.logEvent("INFO", emoji, event, format, args...)
}

func (l *Logger) ErrorEventf(event constants.Event, format string, args ...interface{}) {
	l.logEvent("ERROR", "", event, format, args...)
}

func (l *Logger) Debugf(format string, args ...interface{}) {
	l.log("DEBUG", "", format, args...)
}

func (l *Logger) DebugEnabled() bool {
	return l.settings.Debug
}

func (l *Logger) DeepDebugf(format string, args ...interface{}) {
	l.log("DEEP", "", format, args...)
}

func (l *Logger) DeepDebugEnabled() bool {
	return l.settings.Debug && l.settings.DebugDeep
}

var emojiMap = map[string]string{
	":alarm_clock:":              "⏰",
	":bar_chart:":                "📊",
	":chart_with_upwards_trend:": "📈",
	":four_leaf_clover:":         "🍀",
	":rocket:":                   "🚀",
	":moneybag:":                 "💰",
	":green_circle:":             "🟢",
	":white_check_mark:":         "✅",
	":package:":                  "📦",
	":hourglass:":                "⌛",
	":hourglass_flowing_sand:":   "⏳",
	":speech_balloon:":           "💬",
	":partying_face:":            "🥳",
	":sleeping:":                 "😴",
	":stop_sign:":                "🛑",
	":page_facing_up:":           "📄",
	":gift:":                     "🎁",
	":clipboard:":                "📋",
	":performing_arts:":          "🎭",
	":cry:":                      "😢",
	":disappointed_relieved:":    "😥",
	":video_game:":               "🎮",
	":ambulance:":                "🚑",
}

func emojize(code string) string {
	if val, ok := emojiMap[code]; ok {
		return val
	}
	return code
}
