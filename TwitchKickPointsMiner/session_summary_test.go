package twitchchannelpointsminer

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/classes/entities"
	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/privacy"
)

func TestSessionSummaryCombinesRecordedTwitchAndKickPoints(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerSettings{}, "")
	logger.base.SetOutput(&output)
	live := &entities.Streamer{}
	offline := &entities.Streamer{}
	kickRuntime := &kickAccountRuntime{
		account: KickAccountConfig{Alias: "Test"},
		order:   []string{"live", "unknown", "offline"},
		streamers: map[string]*kickStreamerState{
			"live": {
				Name: "live", Online: true, StatusKnown: true, Watching: true,
				HasPoints: true, InitialPointsSet: true, InitialPoints: 100, Points: 145,
			},
			"unknown": {Name: "unknown", Points: 9999},
			"offline": {
				Name: "offline", StatusKnown: true, HasPoints: true,
				InitialPointsSet: true, InitialPoints: 20, Points: 10,
			},
		},
	}
	now := time.Now()
	miner := &Miner{
		TwitchEnabled: true, logger: logger, startedAt: now.Add(-30 * time.Minute),
		streamers: []*entities.Streamer{live, offline},
		kickMiner: &KickMiner{runtimes: []*kickAccountRuntime{kickRuntime}},
	}
	miner.recordSummaryPresence(live, true)
	miner.recordSummaryPresence(offline, false)
	miner.recordSummaryWatching([]*entities.Streamer{live, offline})
	miner.recordSummaryBalance(live, 1000, 1000)
	miner.recordSummaryBalance(live, 1000, 1100)
	miner.recordSummaryBalance(live, 1100, 1080)
	miner.recordSummaryBalance(offline, 500, 520)
	miner.logSessionSummary(now)
	logs := ansiRegexp.ReplaceAllString(output.String(), "")
	for _, want := range []string{
		"Session summary | Uptime 30m 00s",
		constants.PlatformTwitchToken + " Net points +100 | online 1 of 2 | watching 1",
		constants.PlatformKickToken + " Net points +35 | online 1 of 3 | watching 1",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("summary missing %q:\n%s", want, logs)
		}
	}
}

func TestSessionSummaryIncludesBalanceRefreshesWithoutGainEvents(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerSettings{}, "")
	logger.base.SetOutput(&output)
	streamer := &entities.Streamer{Username: "streamer", ChannelPoints: 1000}
	miner := &Miner{logger: logger, TwitchEnabled: true, streamers: []*entities.Streamer{streamer}}
	miner.handlePointsUpdate(streamer, 0, "")
	streamer.ChannelPoints = 1120
	miner.handlePointsUpdate(streamer, 1000, "")
	miner.logSessionSummary(time.Now())
	logs := ansiRegexp.ReplaceAllString(output.String(), "")
	if !strings.Contains(logs, "Net points +120") {
		t.Fatalf("balance refresh was omitted from the summary: %s", logs)
	}
}

func TestSessionSummaryDropsOfflineChannelsFromActiveWatchCount(t *testing.T) {
	streamer := &entities.Streamer{}
	miner := &Miner{}
	miner.recordSummaryPresence(streamer, true)
	miner.recordSummaryWatching([]*entities.Streamer{streamer})
	miner.recordSummaryPresence(streamer, false)
	if len(miner.summaryWatching) != 0 || miner.summaryOnline[streamer] {
		t.Fatal("offline channel is still counted as watched or online")
	}
}

func TestSessionSummaryHonorsPrivacyAndDisabledPlatforms(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerSettings{}, "")
	logger.base.SetOutput(&output)
	miner := &Miner{
		logger: logger, anonymizer: privacy.New(true),
		kickMiner: &KickMiner{runtimes: []*kickAccountRuntime{{
			order: []string{"private-channel"},
			streamers: map[string]*kickStreamerState{
				"private-channel": {
					Name: "private-channel", HasPoints: true, InitialPointsSet: true, Points: 12345,
				},
			},
		}}},
	}
	miner.logSessionSummary(time.Now())
	logs := output.String()
	if !strings.Contains(logs, "Net points [hidden]") ||
		strings.Contains(logs, "12345") || strings.Contains(logs, "private-channel") ||
		strings.Contains(logs, constants.PlatformTwitchToken) {
		t.Fatalf("summary exposed private data or included a disabled platform:\n%s", logs)
	}
}

func TestSessionSummaryColorsNegativePointChanges(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerSettings{}, "")
	logger.base.SetOutput(&output)
	miner := &Miner{logger: logger}
	miner.logPlatformSessionSummary(constants.PlatformTwitchToken, -25, 1, 2, 1)
	if !strings.Contains(output.String(), constants.ColorRed+"-25"+constants.ColorReset) {
		t.Fatalf("negative net points did not use the existing loss color: %s", output.String())
	}
}
