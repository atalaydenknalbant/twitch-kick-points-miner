package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	miner "github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchChannelPointsMiner"
	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchChannelPointsMiner/classes/entities"
	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchChannelPointsMiner/constants"
	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchChannelPointsMiner/utils"
)

const kickSetupVersion = 5

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info != nil && !info.IsDir()
}

func isGoRunExecutable(path string) bool {
	lower := strings.ToLower(path)
	if strings.Contains(lower, "go-build") {
		return true
	}
	temp := strings.ToLower(os.TempDir())
	return strings.HasPrefix(lower, temp)
}

type appPaths struct {
	WorkDir    string
	ConfigPath string
}

func resolveAppPaths(configFlag, dataDirFlag string) (appPaths, error) {
	if dataDirFlag != "" {
		abs, err := filepath.Abs(dataDirFlag)
		if err != nil {
			return appPaths{}, err
		}
		return appPaths{
			WorkDir:    abs,
			ConfigPath: filepath.Join(abs, "config.json"),
		}, nil
	}

	if configFlag != "" {
		abs, err := filepath.Abs(configFlag)
		if err != nil {
			return appPaths{}, err
		}
		return appPaths{
			WorkDir:    filepath.Dir(abs),
			ConfigPath: abs,
		}, nil
	}

	// ? Preferred environment overrides for Twitch Kick Points Miner.
	if raw := strings.TrimSpace(os.Getenv("TKPM_DATA_DIR")); raw != "" {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return appPaths{}, err
		}
		return appPaths{
			WorkDir:    abs,
			ConfigPath: filepath.Join(abs, "config.json"),
		}, nil
	}

	if raw := strings.TrimSpace(os.Getenv("TKPM_CONFIG")); raw != "" {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return appPaths{}, err
		}
		return appPaths{
			WorkDir:    filepath.Dir(abs),
			ConfigPath: abs,
		}, nil
	}

	// ? Legacy aliases remain supported for existing shortcuts.
	if raw := strings.TrimSpace(os.Getenv("SBPM_DATA_DIR")); raw != "" {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return appPaths{}, err
		}
		return appPaths{
			WorkDir:    abs,
			ConfigPath: filepath.Join(abs, "config.json"),
		}, nil
	}

	if raw := strings.TrimSpace(os.Getenv("SBPM_CONFIG")); raw != "" {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return appPaths{}, err
		}
		return appPaths{
			WorkDir:    filepath.Dir(abs),
			ConfigPath: abs,
		}, nil
	}

	if raw := strings.TrimSpace(os.Getenv("TCPM_DATA_DIR")); raw != "" {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return appPaths{}, err
		}
		return appPaths{
			WorkDir:    abs,
			ConfigPath: filepath.Join(abs, "config.json"),
		}, nil
	}

	if raw := strings.TrimSpace(os.Getenv("TCPM_CONFIG")); raw != "" {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return appPaths{}, err
		}
		return appPaths{
			WorkDir:    filepath.Dir(abs),
			ConfigPath: abs,
		}, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}

	// ? Preserve the historical behavior when the user runs from a folder that already has config.json.
	if cwd != "" && fileExists(filepath.Join(cwd, "config.json")) {
		return appPaths{
			WorkDir:    cwd,
			ConfigPath: filepath.Join(cwd, "config.json"),
		}, nil
	}

	exePath, err := os.Executable()
	if err == nil && exePath != "" && !isGoRunExecutable(exePath) {
		exeDir := filepath.Dir(exePath)
		return appPaths{
			WorkDir:    exeDir,
			ConfigPath: filepath.Join(exeDir, "config.json"),
		}, nil
	}

	// ? Fallback (dev runs, restricted environments).
	if cwd == "" {
		cwd = "."
	}
	return appPaths{
		WorkDir:    cwd,
		ConfigPath: filepath.Join(cwd, "config.json"),
	}, nil
}

func shouldFallbackToUserConfig(err error) bool {
	if err == nil {
		return false
	}
	if os.IsPermission(err) {
		return true
	}
	return errors.Is(err, syscall.EROFS)
}

type filterConditionConfig struct {
	By    string   `json:"by"`
	Where string   `json:"where"`
	Value *float64 `json:"value"`
}

type betConfig struct {
	Strategy           string                 `json:"strategy"`
	Percentage         *int                   `json:"percentage"`
	PercentageGap      *int                   `json:"percentage_gap"`
	MaxPoints          *int                   `json:"max_points"`
	StealthMode        *bool                  `json:"stealth_mode"`
	DeductStakeOnPlace *bool                  `json:"deduct_stake_on_place"`
	DelayMode          string                 `json:"delay_mode"`
	Delay              *float64               `json:"delay"`
	MinimumPoints      *int                   `json:"minimum_points"`
	FilterCondition    *filterConditionConfig `json:"filter_condition"`
}

type streamerSettingsConfig struct {
	MakePredictions *bool     `json:"make_predictions"`
	FollowRaid      *bool     `json:"follow_raid"`
	ClaimDrops      *bool     `json:"claim_drops"`
	ClaimMoments    *bool     `json:"claim_moments"`
	WatchStreak     *bool     `json:"watch_streak"`
	CommunityGoals  *bool     `json:"community_goals"`
	Bet             betConfig `json:"bet"`
	IRCMode         *string   `json:"chat_presence"`
}

type privacyConfig struct {
	AnonymizeLogs bool `json:"anonymize_logs"`
}

type discordConfig struct {
	WebhookAPI string   `json:"webhook_api"`
	Events     []string `json:"events"`
}

type twitchConfig struct {
	Enabled bool `json:"enabled"`
}

type config struct {
	Username                   string             `json:"username"`
	Password                   string             `json:"password"`
	AutoUpdate                 bool               `json:"auto_update"`
	Debug                      bool               `json:"debug"`
	DebugDeep                  bool               `json:"debug_deep"`
	WatchQueueLogging          bool               `json:"watch_queue_logging"`
	SmartLogging               bool               `json:"smart_logging"`
	DisableSSLCertVerification bool               `json:"disable_ssl_cert_verification"`
	ShowSeconds                bool               `json:"show_seconds"`
	ClaimDropsStartup          bool               `json:"claim_drops_startup"`
	ClaimDrops                 bool               `json:"claim_drops"`
	ClaimMoments               bool               `json:"claim_moments"`
	BettingMakePredictions     bool               `json:"betting(make_predictions)"`
	FollowRaid                 bool               `json:"follow_raid"`
	CommunityGoals             bool               `json:"community_goals"`
	Emojis                     bool               `json:"emojis"`
	SaveLogs                   bool               `json:"save_logs"`
	ShowUsernameInConsole      bool               `json:"show_username_in_console"`
	ShowClaimedBonusMsg        bool               `json:"show_claimed_bonus_msg"`
	ShowGame                   bool               `json:"show_game"`
	WatchStreakWarmStartCache  bool               `json:"watch_streak_warm_start_cache"`
	IRCMode                    string             `json:"chat_presence"`
	DisableAtInNickname        bool               `json:"disable_at_in_nickname"`
	ShowDropsProgress          bool               `json:"show_drops_progress"`
	Streamers                  []string           `json:"streamers"`
	StreamersExclude           []string           `json:"streamers_exclude"`
	GamePriority               []string           `json:"game_priority"`
	GameExclude                []string           `json:"game_exclude"`
	WatchPriority              []string           `json:"watch_priority"`
	Bet                        betConfig          `json:"bet"`
	Timezone                   *string            `json:"timezone"`
	Privacy                    privacyConfig      `json:"privacy"`
	Discord                    discordConfig      `json:"discord"`
	Twitch                     twitchConfig       `json:"twitch"`
	Kick                       miner.KickSettings `json:"kick"`

	StreamerOverrides map[string]streamerSettingsConfig `json:"streamer_overrides"`
}

func mergeBetSettings(base entities.BetSettings, override betConfig) entities.BetSettings {
	out := base
	if override.Strategy != "" {
		out.Strategy = entities.Strategy(override.Strategy)
	}
	if override.Percentage != nil {
		out.Percentage = override.Percentage
	}
	if override.PercentageGap != nil {
		out.PercentageGap = override.PercentageGap
	}
	if override.MaxPoints != nil {
		out.MaxPoints = override.MaxPoints
	}
	if override.MinimumPoints != nil {
		out.MinimumPoints = override.MinimumPoints
	}
	if override.StealthMode != nil {
		out.StealthMode = override.StealthMode
	}
	if override.DeductStakeOnPlace != nil {
		out.DeductStakeOnPlace = override.DeductStakeOnPlace
	}
	if override.FilterCondition != nil {
		out.FilterCondition = mergeFilterCondition(out.FilterCondition, override.FilterCondition)
	}
	if override.DelayMode != "" {
		out.DelayMode = entities.DelayMode(override.DelayMode)
	}
	if override.Delay != nil {
		out.Delay = override.Delay
	}
	out.Default()
	return out
}

func mergeStreamerSettings(base entities.StreamerSettings, override streamerSettingsConfig) entities.StreamerSettings {
	out := base
	if override.MakePredictions != nil {
		out.MakePredictions = *override.MakePredictions
	}
	if override.FollowRaid != nil {
		out.FollowRaid = *override.FollowRaid
	}
	if override.ClaimDrops != nil {
		out.ClaimDrops = *override.ClaimDrops
	}
	if override.ClaimMoments != nil {
		out.ClaimMoments = *override.ClaimMoments
	}
	if override.WatchStreak != nil {
		out.WatchStreak = *override.WatchStreak
	}
	if override.CommunityGoals != nil {
		out.CommunityGoals = *override.CommunityGoals
	}
	out.Bet = mergeBetSettings(out.Bet, override.Bet)
	if override.IRCMode != nil {
		out.IRCMode = parseChatPresence(*override.IRCMode, out.IRCMode)
	}
	out.Default()
	return out
}

func parseChatPresence(mode string, fallback entities.IRCMode) entities.IRCMode {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case string(entities.IRCModeAlways):
		return entities.IRCModeAlways
	case string(entities.IRCModeNever):
		return entities.IRCModeNever
	case string(entities.IRCModeOffline):
		return entities.IRCModeOffline
	case string(entities.IRCModeOnline):
		return entities.IRCModeOnline
	default:
		return fallback
	}
}

func mergeFilterCondition(base *entities.FilterCondition, override *filterConditionConfig) *entities.FilterCondition {
	if override == nil {
		return base
	}
	var out entities.FilterCondition
	if base != nil {
		out = *base
	}
	if override.By != "" {
		out.By = entities.OutcomeKey(strings.ToUpper(strings.TrimSpace(override.By)))
	}
	if override.Where != "" {
		out.Where = entities.Condition(strings.ToUpper(strings.TrimSpace(override.Where)))
	}
	if override.Value != nil {
		out.Value = override.Value
	}
	// ? If nothing was set, keep nil to avoid activating an empty filter
	if out.By == "" && out.Where == "" && out.Value == nil {
		return base
	}
	return &out
}

func clearConsole() {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command("cmd", "/c", "cls")
	} else {
		c = exec.Command("clear")
	}
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	_ = c.Run()
}

func setConsoleTitle(title string) {
	if runtime.GOOS != "windows" {
		return
	}
	cmd := exec.Command("cmd", "/c", fmt.Sprintf("title %s", title))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

func defaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"username":                      "your-twitch-username",
		"password":                      "your-twitch-password (Optional)",
		"auto_update":                   true,
		"debug":                         false,
		"debug_deep":                    false,
		"watch_queue_logging":           false,
		"smart_logging":                 true,
		"disable_ssl_cert_verification": false,
		"show_seconds":                  false,
		"claim_drops_startup":           true,
		"claim_drops":                   true,
		"claim_moments":                 true,
		"betting(make_predictions)":     true,
		"follow_raid":                   true,
		"community_goals":               false,
		"emojis":                        true,
		"save_logs":                     false,
		"show_username_in_console":      false,
		"show_claimed_bonus_msg":        true,
		"show_game":                     true,
		"watch_streak_warm_start_cache": true,
		"chat_presence":                 "ONLINE",
		"disable_at_in_nickname":        false,
		"show_drops_progress":           false,
		"timezone":                      nil,
		"privacy": map[string]interface{}{
			"anonymize_logs": false,
		},
		"discord": map[string]interface{}{
			"webhook_api": "",
			"events":      []interface{}{},
		},
		"streamers":         []interface{}{},
		"streamers_exclude": []interface{}{},
		"game_priority":     []interface{}{},
		"game_exclude":      []interface{}{},
		"watch_priority": []interface{}{
			"STREAK",
			"DROPS",
			"ORDER",
		},
		"streamer_overrides": map[string]interface{}{},
		"twitch": map[string]interface{}{
			"enabled": true,
		},
		"kick": map[string]interface{}{
			"enabled":                false,
			"setup_completed":        false,
			"setup_version":          0,
			"check_interval":         120,
			"points_interval":        150,
			"handshake_interval":     30,
			"watch_event_interval":   10,
			"reconnect_cooldown":     60,
			"connection_stagger_min": 3,
			"connection_stagger_max": 8,
			"accounts":               []interface{}{},
		},
		"bet": map[string]interface{}{
			"strategy":              nil,
			"percentage":            nil,
			"percentage_gap":        nil,
			"max_points":            nil,
			"stealth_mode":          nil,
			"deduct_stake_on_place": true,
			"delay_mode":            nil,
			"delay":                 nil,
			"minimum_points":        nil,
			"filter_condition": map[string]interface{}{
				"by":    nil,
				"where": nil,
				"value": nil,
			},
		},
	}
}

func loadOrCreateConfig(path string) (config, error) {
	cfg, _, err := loadOrCreateConfigState(path)
	return cfg, err
}

func loadOrCreateConfigState(path string) (config, bool, error) {
	cfgMap := map[string]interface{}{}
	fileData, err := os.ReadFile(path)
	created := errors.Is(err, os.ErrNotExist)
	if err == nil {
		fileData = bytes.TrimPrefix(fileData, []byte{0xEF, 0xBB, 0xBF})
		if err := json.Unmarshal(fileData, &cfgMap); err != nil {
			return config{}, false, fmt.Errorf("invalid config: %w", err)
		}
	} else if !created {
		return config{}, false, err
	}

	changed := false
	for key, value := range defaultConfig() {
		if _, ok := cfgMap[key]; !ok {
			cfgMap[key] = value
			changed = true
		}
	}

	privacyRaw, ok := cfgMap["privacy"].(map[string]interface{})
	if !ok {
		privacyRaw = defaultConfig()["privacy"].(map[string]interface{})
		cfgMap["privacy"] = privacyRaw
		changed = true
	} else {
		defaultPrivacy := defaultConfig()["privacy"].(map[string]interface{})
		for k, v := range defaultPrivacy {
			if _, ok := privacyRaw[k]; !ok {
				privacyRaw[k] = v
				changed = true
			}
		}
	}

	discordRaw, ok := cfgMap["discord"].(map[string]interface{})
	if !ok {
		discordRaw = defaultConfig()["discord"].(map[string]interface{})
		cfgMap["discord"] = discordRaw
		changed = true
	} else {
		defaultDiscord := defaultConfig()["discord"].(map[string]interface{})
		for k, v := range defaultDiscord {
			if _, ok := discordRaw[k]; !ok {
				discordRaw[k] = v
				changed = true
			}
		}
	}

	betRaw, ok := cfgMap["bet"].(map[string]interface{})
	if !ok {
		betRaw = defaultConfig()["bet"].(map[string]interface{})
		cfgMap["bet"] = betRaw
		changed = true
	} else {
		defaultBet := defaultConfig()["bet"].(map[string]interface{})
		for k, v := range defaultBet {
			if _, ok := betRaw[k]; !ok {
				betRaw[k] = v
				changed = true
			}
		}
		// ? Ensure nested filter_condition keys are present.
		if fcRaw, ok := betRaw["filter_condition"].(map[string]interface{}); ok {
			for k, v := range defaultBet["filter_condition"].(map[string]interface{}) {
				if _, ok := fcRaw[k]; !ok {
					fcRaw[k] = v
					changed = true
				}
			}
		} else {
			betRaw["filter_condition"] = defaultBet["filter_condition"]
			changed = true
		}
	}

	if err != nil || changed {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return config{}, false, err
		}
		if err := utils.SaveJSON(path, cfgMap); err != nil {
			return config{}, false, err
		}
	}

	normalized, err := json.Marshal(cfgMap)
	if err != nil {
		return config{}, false, err
	}
	var cfg config
	if err := json.Unmarshal(normalized, &cfg); err != nil {
		return config{}, false, err
	}
	return cfg, created, nil
}

func configureKickFirstRun(cfg *config, input io.Reader, output io.Writer) error {
	if cfg == nil {
		return errors.New("missing configuration")
	}
	if input == nil {
		input = strings.NewReader("skip\n")
	}
	if output == nil {
		output = io.Discard
	}

	writeKickSetupGuide(output)
	fmt.Fprint(output, "Paste the Kick Authorization header or token, or type Skip: ")

	reader := bufio.NewReader(input)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	choice := strings.TrimSpace(line)
	if strings.EqualFold(choice, "s") || strings.EqualFold(choice, "skip") || choice == "" {
		cfg.Kick.Enabled = false
		cfg.Kick.SetupCompleted = true
		cfg.Kick.SetupVersion = kickSetupVersion
		cfg.Kick.Accounts = nil
		fmt.Fprintln(output, "Kick setup skipped. Run again with -connect-kick to connect it later.")
		return nil
	}
	if kickAuthorizationNeedsContinuation(choice) {
		fmt.Fprint(output, "Paste the token shown after Bearer: ")
		continuation, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		choice += " " + strings.TrimSpace(continuation)
	}
	token := normalizeKickAuthorizationInput(choice)
	if token == "" {
		return errors.New("invalid Kick credential; copy the Authorization: Bearer value from the followed request")
	}

	cfg.Kick.Enabled = true
	cfg.Kick.SetupCompleted = true
	cfg.Kick.SetupVersion = kickSetupVersion
	cfg.Kick.Accounts = []miner.KickAccountConfig{
		{
			Alias:         "Main Kick Account",
			Token:         token,
			Streamers:     []string{},
			MaxConcurrent: 2,
		},
	}
	cfg.Kick.Default()
	fmt.Fprintln(output, "Kick connected. The Bearer prefix was removed automatically. The credential will be saved under cookies, and followed channels will load after Twitch login.")
	return nil
}

func normalizeKickAuthorizationInput(raw string) string {
	value := strings.Trim(strings.TrimSpace(raw), `"'`)
	if index := strings.Index(value, ":"); index >= 0 && strings.EqualFold(strings.TrimSpace(value[:index]), "authorization") {
		value = strings.TrimSpace(value[index+1:])
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	if strings.EqualFold(fields[0], "bearer") {
		fields = fields[1:]
	}
	return strings.Trim(strings.Join(fields, ""), `"'`)
}

func kickAuthorizationNeedsContinuation(raw string) bool {
	value := strings.Trim(strings.TrimSpace(raw), `"'`)
	if index := strings.Index(value, ":"); index >= 0 && strings.EqualFold(strings.TrimSpace(value[:index]), "authorization") {
		value = strings.TrimSpace(value[index+1:])
	}
	return value == "" || strings.EqualFold(value, "bearer")
}

type kickCredentialFile struct {
	Token string `json:"token"`
}

func prepareKickCredentials(cfg *config, baseDir string) (bool, error) {
	if cfg == nil {
		return false, errors.New("missing configuration")
	}
	changed := false
	usedNames := make(map[string]int)
	for index := range cfg.Kick.Accounts {
		account := &cfg.Kick.Accounts[index]
		token := strings.TrimSpace(account.Token)
		placeholder := strings.EqualFold(token, "YOUR_KICK_BEARER_TOKEN")

		if strings.TrimSpace(account.CredentialFile) == "" && token != "" && !placeholder {
			account.CredentialFile = nextKickCredentialFile(account.Alias, usedNames)
			changed = true
		} else if account.CredentialFile != "" {
			usedNames[strings.ToLower(filepath.Base(account.CredentialFile))]++
		}

		if account.CredentialFile == "" || placeholder {
			continue
		}
		credentialPath, err := resolveKickCredentialPath(baseDir, account.CredentialFile)
		if err != nil {
			return changed, fmt.Errorf("Kick account %q credential path: %w", account.Alias, err)
		}
		if token != "" {
			if err := saveKickCredential(credentialPath, token); err != nil {
				return changed, fmt.Errorf("save Kick account %q credential: %w", account.Alias, err)
			}
			continue
		}
		loaded, err := loadKickCredential(credentialPath)
		if err != nil {
			return changed, fmt.Errorf("load Kick account %q credential: %w", account.Alias, err)
		}
		account.Token = loaded
	}
	return changed, nil
}

func nextKickCredentialFile(alias string, used map[string]int) string {
	var builder strings.Builder
	lastUnderscore := false
	for _, char := range strings.ToLower(strings.TrimSpace(alias)) {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if valid {
			builder.WriteRune(char)
			lastUnderscore = false
		} else if !lastUnderscore && builder.Len() > 0 {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	name := strings.Trim(builder.String(), "_")
	if name == "" {
		name = "account"
	}
	base := "kick_" + name
	fileName := base + ".json"
	key := strings.ToLower(fileName)
	if count := used[key]; count > 0 {
		fileName = fmt.Sprintf("%s_%d.json", base, count+1)
	}
	used[key]++
	return filepath.ToSlash(filepath.Join("cookies", fileName))
}

func resolveKickCredentialPath(baseDir, relativePath string) (string, error) {
	if filepath.IsAbs(relativePath) {
		return "", errors.New("credential_file must be relative to the application directory")
	}
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(base, filepath.FromSlash(relativePath)))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(base, path)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("credential_file resolves outside the application directory")
	}
	return path, nil
}

func saveKickCredential(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(kickCredentialFile{Token: token}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func loadKickCredential(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var credential kickCredentialFile
	if err := json.Unmarshal(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}), &credential); err != nil {
		return "", err
	}
	credential.Token = strings.TrimSpace(credential.Token)
	if credential.Token == "" {
		return "", errors.New("credential file contains no token")
	}
	return credential.Token, nil
}

func writeKickSetupGuide(output io.Writer) {
	if output == nil {
		return
	}
	fmt.Fprintf(output, `%s
+------------------------------------------------------------------------+
| KICK CONNECTION SETUP                                                  |
+------------------------------------------------------------------------+
| 1. Open https://kick.com/following in your normal signed in browser.    |
| 2. Press F12, open Network, reload the page, and filter for: followed   |
| 3. Select /api/v2/channels/followed and open Request Headers.           |
| 4. Copy Authorization: Bearer ... and paste it into this terminal.      |
|                                                                        |
| SKIP                                                                   |
| Continues with Twitch only. Kick can be connected on a later launch.   |
+------------------------------------------------------------------------+
%s`, constants.ColorGreen, constants.ColorReset)
}

func configureTwitchFirstRun(cfg *config, input io.Reader, output io.Writer) error {
	if cfg == nil {
		return errors.New("missing configuration")
	}
	if input == nil {
		input = strings.NewReader("connect\n")
	}
	if output == nil {
		output = io.Discard
	}

	fmt.Fprintf(output, `%s
+------------------------------------------------------------------------+
| TWITCH CONNECTION SETUP                                                |
+------------------------------------------------------------------------+
| CONNECT                                                                |
| Uses Twitch device activation after setup, then starts enabled miners. |
|                                                                        |
| SKIP                                                                   |
| Disables Twitch and continues with Kick only.                          |
+------------------------------------------------------------------------+
%s`, constants.ColorPurple, constants.ColorReset)
	fmt.Fprint(output, "Choose Connect or Skip [C/s]: ")

	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	choice := strings.TrimSpace(line)
	switch {
	case choice == "", strings.EqualFold(choice, "c"), strings.EqualFold(choice, "connect"):
		cfg.Twitch.Enabled = true
		fmt.Fprintln(output, "Twitch enabled. Device activation will start after setup.")
		return nil
	case strings.EqualFold(choice, "s"), strings.EqualFold(choice, "skip"):
		cfg.Twitch.Enabled = false
		fmt.Fprintln(output, "Twitch skipped. The miner will run Kick only.")
		return nil
	default:
		return fmt.Errorf("unknown Twitch setup choice %q; enter Connect or Skip", choice)
	}
}

func kickSetupNeeded(settings miner.KickSettings) bool {
	hasConfiguredToken := false
	for _, account := range settings.Accounts {
		token := strings.TrimSpace(account.Token)
		if strings.EqualFold(token, "YOUR_KICK_BEARER_TOKEN") {
			return true
		}
		if token != "" {
			hasConfiguredToken = true
		}
	}
	if hasConfiguredToken {
		return false
	}
	return !settings.SetupCompleted || settings.SetupVersion < kickSetupVersion
}

func applyTimezoneOverride(raw *string, logger *miner.Logger) {
	if raw == nil {
		return
	}
	zone := strings.TrimSpace(*raw)
	if zone == "" || strings.EqualFold(zone, "auto") {
		return
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		logger.Errorf("%sTimezone override ignored; falling back to system time: %v%s", constants.ColorRed, err, constants.ColorReset)
	}
	time.Local = loc
}

func buildBaseStreamerSettings(cfg config) entities.StreamerSettings {
	betSettings := entities.BetSettings{
		Strategy:           entities.Strategy(cfg.Bet.Strategy),
		Percentage:         cfg.Bet.Percentage,
		PercentageGap:      cfg.Bet.PercentageGap,
		MaxPoints:          cfg.Bet.MaxPoints,
		StealthMode:        cfg.Bet.StealthMode,
		DeductStakeOnPlace: cfg.Bet.DeductStakeOnPlace,
		DelayMode:          entities.DelayMode(cfg.Bet.DelayMode),
		Delay:              cfg.Bet.Delay,
		MinimumPoints:      cfg.Bet.MinimumPoints,
		FilterCondition:    mergeFilterCondition(nil, cfg.Bet.FilterCondition),
	}
	betSettings.Default()

	streamerSettings := entities.StreamerSettings{
		MakePredictions: cfg.BettingMakePredictions,
		FollowRaid:      cfg.FollowRaid,
		ClaimDrops:      cfg.ClaimDrops,
		ClaimMoments:    cfg.ClaimMoments,
		WatchStreak:     true,
		CommunityGoals:  cfg.CommunityGoals,
		Bet:             betSettings,
		IRCMode:         parseChatPresence(cfg.IRCMode, entities.IRCModeOnline),
	}
	streamerSettings.Default()
	return streamerSettings
}

func buildOverrideSettings(base entities.StreamerSettings, overrides map[string]streamerSettingsConfig) map[string]entities.StreamerSettings {
	overrideSettings := make(map[string]entities.StreamerSettings, len(overrides))
	for name, override := range overrides {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		overrideSettings[key] = mergeStreamerSettings(base, override)
	}
	return overrideSettings
}

func main() {
	handled, remainingArgs, err := miner.HandleUpdateStartup(os.Args[1:])
	if err != nil {
		log.Fatalf("update helper failed: %v", err)
	}
	if handled {
		return
	}
	os.Args = append([]string{os.Args[0]}, remainingArgs...)

	configFlag := flag.String("config", "", "Path to config.json (default: ./config.json or next to the executable)")
	dataDirFlag := flag.String("data-dir", "", "Directory for config/cookies/log (default: current directory if config.json exists; otherwise the executable directory)")
	connectKickFlag := flag.Bool("connect-kick", false, "Run the guided Kick credential setup")
	setupTwitchFlag := flag.Bool("setup-twitch", false, "Run the Twitch Connect or Skip setup")
	flag.Parse()

	hasOverride := *configFlag != "" || *dataDirFlag != "" || strings.TrimSpace(os.Getenv("TKPM_CONFIG")) != "" || strings.TrimSpace(os.Getenv("TKPM_DATA_DIR")) != "" || strings.TrimSpace(os.Getenv("SBPM_CONFIG")) != "" || strings.TrimSpace(os.Getenv("SBPM_DATA_DIR")) != "" || strings.TrimSpace(os.Getenv("TCPM_CONFIG")) != "" || strings.TrimSpace(os.Getenv("TCPM_DATA_DIR")) != ""
	paths, err := resolveAppPaths(*configFlag, *dataDirFlag)
	if err != nil {
		log.Fatalf("failed to resolve config paths: %v", err)
	}
	if paths.WorkDir != "" {
		_ = os.MkdirAll(paths.WorkDir, 0o755)
		if err := os.Chdir(paths.WorkDir); err != nil {
			log.Printf("warning: failed to change working directory to %q: %v", paths.WorkDir, err)
		}
	}

	setConsoleTitle(constants.ProductName)
	clearConsole()
	writeStartupArt(os.Stdout)
	cfg, configCreated, err := loadOrCreateConfigState(paths.ConfigPath)
	if err != nil && !hasOverride && shouldFallbackToUserConfig(err) {
		if base, derr := os.UserConfigDir(); derr == nil && base != "" {
			fallbackDir := filepath.Join(base, "TwitchKickPointsMiner")
			_ = os.MkdirAll(fallbackDir, 0o755)
			if chErr := os.Chdir(fallbackDir); chErr == nil {
				fallbackCfg := filepath.Join(fallbackDir, "config.json")
				if cfg2, created2, err2 := loadOrCreateConfigState(fallbackCfg); err2 == nil {
					cfg = cfg2
					configCreated = created2
					err = nil
					paths.ConfigPath = fallbackCfg
					log.Printf("using config directory %q", fallbackDir)
				}
			}
		}
	}
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	credentialChanged, credentialErr := prepareKickCredentials(&cfg, filepath.Dir(paths.ConfigPath))
	if credentialErr != nil && !*connectKickFlag {
		log.Fatalf("failed to load Kick credentials: %v; run again with -connect-kick to replace them", credentialErr)
	}
	if credentialErr != nil {
		log.Printf("existing Kick credential ignored during reconnect: %v", credentialErr)
	}
	setupChanged := false
	if configCreated || *connectKickFlag || kickSetupNeeded(cfg.Kick) {
		if err := configureKickFirstRun(&cfg, os.Stdin, os.Stdout); err != nil {
			log.Fatalf("Kick setup failed: %v", err)
		}
		setupChanged = true
	}
	if configCreated || *setupTwitchFlag {
		if err := configureTwitchFirstRun(&cfg, os.Stdin, os.Stdout); err != nil {
			log.Fatalf("Twitch setup failed: %v", err)
		}
		setupChanged = true
	}
	if setupChanged {
		migrated, migrationErr := prepareKickCredentials(&cfg, filepath.Dir(paths.ConfigPath))
		if migrationErr != nil {
			log.Fatalf("failed to save Kick credentials: %v", migrationErr)
		}
		credentialChanged = credentialChanged || migrated
	}
	if setupChanged || credentialChanged {
		if err := utils.SaveJSON(paths.ConfigPath, cfg); err != nil {
			log.Fatalf("failed to save first run configuration: %v", err)
		}
	}
	if !cfg.Twitch.Enabled && !cfg.Kick.Enabled {
		fmt.Println("Twitch and Kick are both disabled. Run with -setup-twitch or -connect-kick to enable a platform.")
		return
	}
	if cfg.AutoUpdate {
		updated, updateErr := miner.RunAutoUpdate()
		if updateErr != nil {
			log.Printf("update check failed: %v", updateErr)
		}
		if updated {
			log.Printf("verified update installed; restarting")
			return
		}
	}

	// ? Apply optional defaults/overrides (per-streamer)
	baseStreamerSettings := buildBaseStreamerSettings(cfg)
	overrideSettings := buildOverrideSettings(baseStreamerSettings, cfg.StreamerOverrides)
	cfg.Kick.Default()

	loggerSettings := miner.LoggerSettings{
		Save:             cfg.SaveLogs,
		ConsoleLevel:     0,
		FileLevel:        0,
		Emoji:            cfg.Emojis,
		Smart:            cfg.SmartLogging,
		ShowSeconds:      cfg.ShowSeconds,
		ConsoleUsername:  cfg.ShowUsernameInConsole,
		ShowClaimedBonus: cfg.ShowClaimedBonusMsg,
		Less:             false,
		Debug:            cfg.Debug,
		DebugDeep:        cfg.DebugDeep,
		AnonymizeLogs:    cfg.Privacy.AnonymizeLogs,
		Discord: miner.DiscordSettings{
			WebhookAPI: cfg.Discord.WebhookAPI,
			Events:     cfg.Discord.Events,
		},
	}

	logger := miner.NewLogger(loggerSettings, cfg.Username)
	applyTimezoneOverride(cfg.Timezone, logger)

	minr := miner.NewMiner(
		cfg.Username,
		cfg.Password,
		cfg.ClaimDropsStartup,
		cfg.DisableSSLCertVerification,
		loggerSettings,
		baseStreamerSettings,
		overrideSettings,
		cfg.WatchPriority,
		cfg.StreamersExclude,
		cfg.GamePriority,
		cfg.GameExclude,
		cfg.DisableAtInNickname,
		cfg.ShowGame,
		cfg.WatchQueueLogging,
		cfg.WatchStreakWarmStartCache,
		cfg.ShowDropsProgress,
	)
	minr.SetTwitchEnabled(cfg.Twitch.Enabled)
	minr.SetKickSettings(cfg.Kick)

	if !cfg.Twitch.Enabled {
		minr.MineKickOnly()
	} else if len(cfg.Streamers) > 0 {
		minr.Mine(cfg.Streamers)
	} else {
		minr.MineFollowers(entities.FollowersOrderDESC)
	}
}
