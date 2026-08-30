package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	miner "github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner"
	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/classes/entities"
)

func TestDefaultConfigIncludesExpectedKeys(t *testing.T) {
	cfg := defaultConfig()
	required := []string{
		"username",
		"password",
		"twitch",
		"auto_update",
		"claim_moments",
		"streamers",
		"game_priority",
		"chat_presence",
		"disable_at_in_nickname",
		"show_drops_progress",
		"bet",
		"kick",
		"watch_priority",
	}
	for _, key := range required {
		if _, ok := cfg[key]; !ok {
			t.Fatalf("missing key %q in default config", key)
		}
	}
	if got, ok := cfg["show_drops_progress"].(bool); !ok || got {
		t.Fatalf("show_drops_progress default got %#v, want false", cfg["show_drops_progress"])
	}
	if got, ok := cfg["auto_update"].(bool); !ok || !got {
		t.Fatalf("auto_update default got %#v, want true", cfg["auto_update"])
	}
	twitch, ok := cfg["twitch"].(map[string]interface{})
	if !ok || twitch["enabled"] != true {
		t.Fatalf("twitch.enabled default got %#v, want true", cfg["twitch"])
	}
}

func TestConfigureTwitchFirstRunCanConnect(t *testing.T) {
	cfg := config{}
	var output bytes.Buffer
	if err := configureTwitchFirstRun(&cfg, strings.NewReader("connect\n"), &output); err != nil {
		t.Fatalf("connect Twitch: %v", err)
	}
	if !cfg.Twitch.Enabled {
		t.Fatal("Twitch should be enabled")
	}
	if !strings.Contains(output.String(), "Device activation will start after setup") {
		t.Fatalf("Twitch confirmation missing: %s", output.String())
	}
}

func TestConfigureTwitchFirstRunCanSkip(t *testing.T) {
	cfg := config{Twitch: twitchConfig{Enabled: true}}
	var output bytes.Buffer
	if err := configureTwitchFirstRun(&cfg, strings.NewReader("skip\n"), &output); err != nil {
		t.Fatalf("skip Twitch: %v", err)
	}
	if cfg.Twitch.Enabled {
		t.Fatal("Twitch should be disabled")
	}
	if !strings.Contains(output.String(), "run Kick only") {
		t.Fatalf("Twitch skip confirmation missing: %s", output.String())
	}
}

func TestLoadOrCreateConfigCreatesFileAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")

	cfg, err := loadOrCreateConfig(path)
	if err != nil {
		t.Fatalf("load/create config error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file should be created: %v", err)
	}
	if cfg.WatchPriority == nil || len(cfg.WatchPriority) == 0 {
		t.Fatalf("watch priority should have defaults: %#v", cfg)
	}
	if !cfg.Twitch.Enabled {
		t.Fatal("Twitch should default to enabled")
	}
}

func TestLoadOrCreateConfigStateReportsNewConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")

	_, created, err := loadOrCreateConfigState(path)
	if err != nil {
		t.Fatalf("load/create config error: %v", err)
	}
	if !created {
		t.Fatal("missing config should be reported as newly created")
	}

	_, created, err = loadOrCreateConfigState(path)
	if err != nil {
		t.Fatalf("reload config error: %v", err)
	}
	if created {
		t.Fatal("existing config should not be reported as newly created")
	}
}

func TestConfigureKickFirstRunEnablesFollowedMode(t *testing.T) {
	cfg := config{}
	var output bytes.Buffer
	if err := configureKickFirstRun(&cfg, strings.NewReader("Authorization: Bearer test-kick-token\n"), &output); err != nil {
		t.Fatalf("configure Kick: %v", err)
	}
	if !cfg.Kick.Enabled {
		t.Fatal("Kick should be enabled")
	}
	if !cfg.Kick.SetupCompleted {
		t.Fatal("Kick setup should be marked complete")
	}
	if cfg.Kick.SetupVersion != kickSetupVersion {
		t.Fatalf("Kick setup version got %d", cfg.Kick.SetupVersion)
	}
	if len(cfg.Kick.Accounts) != 1 {
		t.Fatalf("Kick account count got %d", len(cfg.Kick.Accounts))
	}
	account := cfg.Kick.Accounts[0]
	if account.Token != "test-kick-token" {
		t.Fatalf("Kick token was not normalized")
	}
	if len(account.Streamers) != 0 {
		t.Fatalf("empty streamer list should enable followed mode: %#v", account.Streamers)
	}
	if account.MaxConcurrent != 2 {
		t.Fatalf("max concurrent got %d", account.MaxConcurrent)
	}
	if !strings.Contains(output.String(), "credential will be saved under cookies") {
		t.Fatalf("setup confirmation missing: %s", output.String())
	}
}

func TestConfigureKickFirstRunCanSkip(t *testing.T) {
	cfg := config{
		Kick: miner.KickSettings{
			Enabled: true,
			Accounts: []miner.KickAccountConfig{
				{Token: "old-token"},
			},
		},
	}
	var output bytes.Buffer
	if err := configureKickFirstRun(&cfg, strings.NewReader("skip\n"), &output); err != nil {
		t.Fatalf("skip Kick setup: %v", err)
	}
	if cfg.Kick.Enabled || !cfg.Kick.SetupCompleted || cfg.Kick.SetupVersion != kickSetupVersion || len(cfg.Kick.Accounts) != 0 {
		t.Fatalf("Kick should be disabled after skip: %#v", cfg.Kick)
	}
	if !strings.Contains(output.String(), "Kick setup skipped") {
		t.Fatalf("skip confirmation missing: %s", output.String())
	}
}

func TestNormalizeKickAuthorizationInput(t *testing.T) {
	tests := map[string]string{
		"Authorization: Bearer token-123": "token-123",
		"Bearer token-456":                "token-456",
		"token-789":                       "token-789",
		"Bearer\nwrapped-token-101":       "wrapped-token-101",
		"Bearer":                          "",
	}
	for input, expected := range tests {
		if got := normalizeKickAuthorizationInput(input); got != expected {
			t.Errorf("normalizeKickAuthorizationInput(%q) got %q, want %q", input, got, expected)
		}
	}
}

func TestConfigureKickFirstRunAcceptsWrappedBearerValue(t *testing.T) {
	cfg := config{}
	var output bytes.Buffer
	if err := configureKickFirstRun(&cfg, strings.NewReader("Bearer\nwrapped-token-123\n"), &output); err != nil {
		t.Fatalf("configure wrapped Kick credential: %v", err)
	}
	if len(cfg.Kick.Accounts) != 1 || cfg.Kick.Accounts[0].Token != "wrapped-token-123" {
		t.Fatalf("wrapped Kick credential was not normalized: %#v", cfg.Kick.Accounts)
	}
	if !strings.Contains(output.String(), "Paste the token shown after Bearer") {
		t.Fatalf("continuation prompt missing: %s", output.String())
	}
}

func TestKickSetupGuideDirectsUserToFollowedRequest(t *testing.T) {
	var output bytes.Buffer
	writeKickSetupGuide(&output)
	guide := output.String()
	for _, expected := range []string{
		"KICK CONNECTION SETUP",
		"https://kick.com/following",
		"/api/v2/channels/followed",
		"Request Headers",
		"Authorization: Bearer",
		"SKIP",
	} {
		if !strings.Contains(guide, expected) {
			t.Fatalf("Kick setup guide missing %q", expected)
		}
	}
}

func TestKickSetupNeeded(t *testing.T) {
	tests := []struct {
		name     string
		settings miner.KickSettings
		want     bool
	}{
		{name: "new configuration", settings: miner.KickSettings{}, want: true},
		{name: "placeholder token", settings: miner.KickSettings{Accounts: []miner.KickAccountConfig{{Token: "YOUR_KICK_BEARER_TOKEN"}}}, want: true},
		{name: "completed placeholder migration", settings: miner.KickSettings{SetupCompleted: true, Accounts: []miner.KickAccountConfig{{Token: "YOUR_KICK_BEARER_TOKEN"}}}, want: true},
		{name: "old skipped setup", settings: miner.KickSettings{SetupCompleted: true}, want: true},
		{name: "current skipped setup", settings: miner.KickSettings{SetupCompleted: true, SetupVersion: kickSetupVersion}, want: false},
		{name: "existing token", settings: miner.KickSettings{Accounts: []miner.KickAccountConfig{{Token: "existing-token"}}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := kickSetupNeeded(test.settings); got != test.want {
				t.Fatalf("kickSetupNeeded got %v, want %v", got, test.want)
			}
		})
	}
}

func TestPrepareKickCredentialsMigratesConfigToken(t *testing.T) {
	dir := t.TempDir()
	cfg := config{Kick: miner.KickSettings{Accounts: []miner.KickAccountConfig{{
		Alias:         "Main Kick Account",
		Token:         "secret-kick-token-123",
		MaxConcurrent: 2,
	}}}}

	changed, err := prepareKickCredentials(&cfg, dir)
	if err != nil {
		t.Fatalf("prepare Kick credentials: %v", err)
	}
	if !changed {
		t.Fatal("legacy Kick token should require config migration")
	}
	account := cfg.Kick.Accounts[0]
	if account.CredentialFile != "cookies/kick_main_kick_account.json" {
		t.Fatalf("credential file got %q", account.CredentialFile)
	}
	credentialPath := filepath.Join(dir, filepath.FromSlash(account.CredentialFile))
	stored, err := loadKickCredential(credentialPath)
	if err != nil {
		t.Fatalf("load migrated Kick credential: %v", err)
	}
	if stored != "secret-kick-token-123" {
		t.Fatalf("stored Kick token got %q", stored)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal migrated config: %v", err)
	}
	if strings.Contains(string(raw), "secret-kick-token-123") || strings.Contains(string(raw), `"token"`) {
		t.Fatalf("serialized config contains Kick token: %s", raw)
	}

	var reloaded config
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatalf("unmarshal migrated config: %v", err)
	}
	if _, err := prepareKickCredentials(&reloaded, dir); err != nil {
		t.Fatalf("reload Kick credentials: %v", err)
	}
	if reloaded.Kick.Accounts[0].Token != "secret-kick-token-123" {
		t.Fatal("Kick token was not restored from credential file")
	}
}

func TestKickAccountConfigReadsLegacyTokenWithoutSerializingIt(t *testing.T) {
	var account miner.KickAccountConfig
	if err := json.Unmarshal([]byte(`{"alias":"Legacy","token":"legacy-secret","streamers":[]}`), &account); err != nil {
		t.Fatalf("unmarshal legacy Kick account: %v", err)
	}
	if account.Token != "legacy-secret" {
		t.Fatalf("legacy Kick token got %q", account.Token)
	}
	raw, err := json.Marshal(account)
	if err != nil {
		t.Fatalf("marshal Kick account: %v", err)
	}
	if strings.Contains(string(raw), "legacy-secret") || strings.Contains(string(raw), `"token"`) {
		t.Fatalf("Kick account serialized a secret: %s", raw)
	}
}

func TestLoadOrCreateConfigPreservesAutoUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{"auto_update":true}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadOrCreateConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.AutoUpdate {
		t.Fatalf("auto_update should remain enabled")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var saved map[string]interface{}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("decode saved config: %v", err)
	}
	if enabled, exists := saved["auto_update"].(bool); !exists || !enabled {
		t.Fatalf("auto_update should be preserved")
	}
}

func TestApplyTimezoneOverride(t *testing.T) {
	original := time.Local
	defer func() { time.Local = original }()

	zone := "UTC"
	logger := miner.NewLogger(miner.LoggerSettings{}, "")
	applyTimezoneOverride(&zone, logger)
	if time.Local.String() != "UTC" {
		t.Fatalf("expected time.Local set to UTC, got %s", time.Local.String())
	}

	bad := "Bad/Zone"
	applyTimezoneOverride(&bad, logger)
	if time.Local.String() != "UTC" {
		t.Fatalf("invalid timezone should not change location, got %s", time.Local.String())
	}
}

func TestBuildBaseStreamerSettingsAppliesGlobalFilterCondition(t *testing.T) {
	cfg := config{
		BettingMakePredictions: true,
		FollowRaid:             true,
		ClaimDrops:             true,
		CommunityGoals:         false,
		IRCMode:                "ONLINE",
		Bet: betConfig{
			FilterCondition: &filterConditionConfig{
				By:    "TOTAL_USERS",
				Where: "GTE",
				Value: func() *float64 { v := 500000.0; return &v }(),
			},
		},
	}

	base := buildBaseStreamerSettings(cfg)
	if base.Bet.FilterCondition == nil {
		t.Fatalf("expected global filter_condition applied to base streamer settings")
	}
	if base.Bet.FilterCondition.By != "TOTAL_USERS" {
		t.Fatalf("expected By TOTAL_USERS, got %s", base.Bet.FilterCondition.By)
	}
	if base.Bet.FilterCondition.Where != "GTE" {
		t.Fatalf("expected Where GTE, got %s", base.Bet.FilterCondition.Where)
	}
	if base.Bet.FilterCondition.Value == nil || *base.Bet.FilterCondition.Value != 500000.0 {
		t.Fatalf("expected Value 500000, got %#v", base.Bet.FilterCondition.Value)
	}
}

func TestBuildBaseStreamerSettingsUsesGlobalClaimMoments(t *testing.T) {
	cfg := config{
		BettingMakePredictions: true,
		FollowRaid:             true,
		ClaimDrops:             true,
		ClaimMoments:           false,
		CommunityGoals:         false,
		IRCMode:                "ONLINE",
	}

	base := buildBaseStreamerSettings(cfg)
	if base.ClaimMoments {
		t.Fatalf("expected base claim_moments false from global config")
	}
}

func TestBuildOverrideSettingsMergesFilterCondition(t *testing.T) {
	base := entities.StreamerSettings{
		MakePredictions: true,
		Bet: entities.BetSettings{
			Strategy: entities.StrategySmart,
		},
	}
	base.Default()

	overrides := map[string]streamerSettingsConfig{
		"SomeStreamer": {
			Bet: betConfig{
				FilterCondition: &filterConditionConfig{
					By:    "TOTAL_POINTS",
					Where: "GT",
					Value: func() *float64 { v := 999999.0; return &v }(),
				},
			},
		},
	}

	merged := buildOverrideSettings(base, overrides)
	override, ok := merged["somestreamer"]
	if !ok {
		t.Fatalf("expected override settings keyed by lowercased streamer name")
	}
	if override.Bet.FilterCondition == nil {
		t.Fatalf("expected override filter_condition merged into settings")
	}
	if override.Bet.FilterCondition.By != "TOTAL_POINTS" || override.Bet.FilterCondition.Where != "GT" {
		t.Fatalf("expected override filter_condition TOTAL_POINTS GT, got %v %v", override.Bet.FilterCondition.By, override.Bet.FilterCondition.Where)
	}
	if override.Bet.FilterCondition.Value == nil || *override.Bet.FilterCondition.Value != 999999.0 {
		t.Fatalf("expected override Value 999999, got %#v", override.Bet.FilterCondition.Value)
	}
}

func TestBuildOverrideSettingsCanEnableClaimMomentsOverGlobalDefault(t *testing.T) {
	base := entities.StreamerSettings{
		ClaimMoments: false,
	}
	base.Default()

	enable := true
	overrides := map[string]streamerSettingsConfig{
		"SomeStreamer": {
			ClaimMoments: &enable,
		},
	}

	merged := buildOverrideSettings(base, overrides)
	got, ok := merged["somestreamer"]
	if !ok {
		t.Fatalf("expected override for somestreamer")
	}
	if !got.ClaimMoments {
		t.Fatalf("expected streamer override to enable claim_moments")
	}
}
