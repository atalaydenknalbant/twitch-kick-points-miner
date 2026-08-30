package twitchchannelpointsminer

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type kickRoundTripFunc func(*http.Request) (*http.Response, error)

func (f kickRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestParseKickChannelInfoSupportsWrappedResponse(t *testing.T) {
	info, err := parseKickChannelInfo(map[string]interface{}{
		"data": map[string]interface{}{
			"id":      float64(123),
			"user_id": float64(456),
			"livestream": map[string]interface{}{
				"id": float64(789),
			},
		},
	})
	if err != nil {
		t.Fatalf("parse channel info: %v", err)
	}
	if info.ChannelID != 123 || info.UserID != 456 || info.StreamID != 789 || !info.Online {
		t.Fatalf("unexpected channel info: %#v", info)
	}
}

func TestParseKickChannelInfoSupportsFlatResponseWithNestedUser(t *testing.T) {
	info, err := parseKickChannelInfo(map[string]interface{}{
		"id": "321",
		"user": map[string]interface{}{
			"id": "654",
		},
		"livestream": map[string]interface{}{
			"data": map[string]interface{}{
				"id": "987",
			},
		},
	})
	if err != nil {
		t.Fatalf("parse channel info: %v", err)
	}
	if info.ChannelID != 321 || info.UserID != 654 || info.StreamID != 987 || !info.Online {
		t.Fatalf("unexpected channel info: %#v", info)
	}
}

func TestParseKickPointsSupportsKnownShapes(t *testing.T) {
	points, ok := parseKickPoints(map[string]interface{}{
		"data": map[string]interface{}{
			"points": float64(111),
		},
	})
	if !ok || points != 111 {
		t.Fatalf("wrapped points got %d %v", points, ok)
	}

	points, ok = parseKickPoints(map[string]interface{}{
		"points": "222",
	})
	if !ok || points != 222 {
		t.Fatalf("flat points got %d %v", points, ok)
	}
}

func TestParseKickFollowedChannelsPage(t *testing.T) {
	names, cursor, err := parseKickFollowedChannelsPage(map[string]interface{}{
		"nextCursor": float64(5),
		"channels": []interface{}{
			map[string]interface{}{"channel_slug": "FirstChannel"},
			map[string]interface{}{"user_username": "SecondChannel"},
			map[string]interface{}{"channel_slug": "firstchannel"},
		},
	})
	if err != nil {
		t.Fatalf("parse followed channels: %v", err)
	}
	if cursor != "5" {
		t.Fatalf("cursor got %q", cursor)
	}
	if len(names) != 2 || names[0] != "firstchannel" || names[1] != "secondchannel" {
		t.Fatalf("followed channels got %#v", names)
	}
}

func TestParseKickFollowedChannelsPageSupportsWrappedNestedChannels(t *testing.T) {
	names, cursor, err := parseKickFollowedChannelsPage(map[string]interface{}{
		"data": map[string]interface{}{
			"next_cursor": "next-page",
			"channels": []interface{}{
				map[string]interface{}{
					"channel": map[string]interface{}{"slug": "NestedChannel"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("parse wrapped followed channels: %v", err)
	}
	if cursor != "next-page" || len(names) != 1 || names[0] != "nestedchannel" {
		t.Fatalf("unexpected wrapped page: names=%#v cursor=%q", names, cursor)
	}
}

func TestParseKickFollowedChannelsPageRejectsMissingChannels(t *testing.T) {
	if _, _, err := parseKickFollowedChannelsPage(map[string]interface{}{}); err == nil {
		t.Fatal("expected missing channels error")
	}
}

func TestKickClientLoadsEveryFollowedChannelPage(t *testing.T) {
	client := newKickClient("test-token")
	client.initialized = true
	requestedCursors := make([]string, 0, 2)
	client.httpClient.Transport = kickRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization header got %q", got)
		}
		cursor := req.URL.Query().Get("cursor")
		requestedCursors = append(requestedCursors, cursor)

		body := `{"nextCursor":2,"channels":[{"channel_slug":"First"},{"channel_slug":"Second"}]}`
		if cursor == "2" {
			body = `{"nextCursor":null,"channels":[{"channel_slug":"Second"},{"channel_slug":"Third"}]}`
		} else if cursor != "" {
			t.Fatalf("unexpected cursor %q", cursor)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})

	names, err := client.followedChannels(context.Background())
	if err != nil {
		t.Fatalf("load followed channels: %v", err)
	}
	if len(requestedCursors) != 2 || requestedCursors[0] != "" || requestedCursors[1] != "2" {
		t.Fatalf("requested cursors got %#v", requestedCursors)
	}
	if len(names) != 3 || names[0] != "first" || names[1] != "second" || names[2] != "third" {
		t.Fatalf("followed channels got %#v", names)
	}
}

func TestKickRefreshReportsEveryFollowedChannel(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerSettings{Emoji: true}, "")
	logger.base.SetOutput(&output)
	runtime := newKickAccountRuntime(KickAccountConfig{
		Alias:         "Test",
		Token:         "test-token",
		MaxConcurrent: 2,
	}, KickSettings{}, logger)
	runtime.client.initialized = true
	runtime.client.httpClient.Transport = kickRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		status := http.StatusOK
		body := `{}`
		switch req.URL.Path {
		case "/api/v2/channels/followed":
			body = `{"nextCursor":null,"channels":[{"channel_slug":"Online"},{"channel_slug":"Offline"},{"channel_slug":"Unavailable"}]}`
		case "/api/v2/channels/online":
			body = `{"id":1,"user_id":11,"livestream":{"id":101}}`
		case "/api/v2/channels/offline":
			body = `{"id":2,"user_id":22,"livestream":null}`
		case "/api/v2/channels/unavailable":
			status = http.StatusInternalServerError
			body = `{"error":"temporary failure"}`
		case "/api/v2/channels/online/points":
			body = `{"data":{"points":100}}`
		case "/api/v2/channels/offline/points":
			body = `{"data":{"points":20}}`
		case "/api/v2/channels/unavailable/points":
			body = `{"data":{"points":30}}`
		case "/api/v2/channels/offline/livestream":
			status = http.StatusNotFound
		default:
			t.Fatalf("unexpected Kick request path %q", req.URL.Path)
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})

	runtime.refresh(context.Background())
	runtime.logLoaded(0)

	logs := output.String()
	for _, expected := range []string{
		"Loading data for 3 streamer(s). Please wait...",
		"Online (100 points) is Online!",
		"Offline (20 points) is Offline!",
		"Unavailable (30 points) status is Unavailable!",
		"3 Streamer loaded!",
	} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("Kick startup output missing %q:\n%s", expected, logs)
		}
	}

	snapshot := runtime.snapshot()
	if len(snapshot.Streamers) != 3 {
		t.Fatalf("followed streamer count got %d", len(snapshot.Streamers))
	}
	if !snapshot.Streamers[0].StatusKnown || !snapshot.Streamers[0].Online {
		t.Fatalf("online streamer state got %#v", snapshot.Streamers[0])
	}
	if !snapshot.Streamers[1].StatusKnown || snapshot.Streamers[1].Online {
		t.Fatalf("offline streamer state got %#v", snapshot.Streamers[1])
	}
	if snapshot.Streamers[2].StatusKnown {
		t.Fatalf("failed status request should remain unavailable: %#v", snapshot.Streamers[2])
	}
}

func TestKickTransientStatusAndHTMLHelpers(t *testing.T) {
	if !isKickTransientStatus(http.StatusBadGateway) || !isKickTransientStatus(http.StatusInternalServerError) {
		t.Fatalf("expected 500 and 502 to be transient")
	}
	if isKickTransientStatus(http.StatusUnauthorized) {
		t.Fatalf("401 should not be treated as transient")
	}
	if !looksLikeHTML([]byte("<html><body>bad gateway</body></html>")) {
		t.Fatalf("expected html body detection")
	}
	if looksLikeHTML([]byte(`{"data":{"points":10}}`)) {
		t.Fatalf("json should not be detected as html")
	}
}

func TestKickSettingsDefaultNormalizesAccounts(t *testing.T) {
	settings := KickSettings{
		Enabled: true,
		Accounts: []KickAccountConfig{
			{
				Token:     "token",
				Streamers: []string{"Streamer", "streamer", "Other"},
			},
		},
	}
	settings.Default()
	if settings.CheckIntervalSeconds != 120 {
		t.Fatalf("check interval got %d", settings.CheckIntervalSeconds)
	}
	if settings.Accounts[0].Alias == "" {
		t.Fatalf("expected alias default")
	}
	if settings.Accounts[0].MaxConcurrent != 2 {
		t.Fatalf("max concurrent got %d", settings.Accounts[0].MaxConcurrent)
	}
	if got := settings.Accounts[0].Streamers; len(got) != 2 || got[0] != "streamer" || got[1] != "other" {
		t.Fatalf("streamers not normalized: %#v", got)
	}
}

func TestKickRuntimeTracksInitialPointsAndHistory(t *testing.T) {
	runtime := newKickAccountRuntime(KickAccountConfig{
		Alias:     "Test",
		Token:     "token",
		Streamers: []string{"Streamer"},
	}, KickSettings{}, NewLogger(LoggerSettings{}, ""))

	runtime.updateKickPoints("streamer", 100, "")
	snapshot := runtime.snapshot()
	if len(snapshot.Streamers) != 1 {
		t.Fatalf("streamer count got %d", len(snapshot.Streamers))
	}
	streamer := snapshot.Streamers[0]
	if !streamer.HasPoints || !streamer.InitialPointsSet || streamer.Points != 100 || streamer.InitialPoints != 100 {
		t.Fatalf("unexpected initial tracker state: %#v", streamer)
	}
	if len(streamer.History) != 0 {
		t.Fatalf("first balance read should not create history: %#v", streamer.History)
	}

	runtime.updateKickPoints("streamer", 115, "")
	runtime.updateKickPoints("streamer", 110, "")

	streamer = runtime.snapshot().Streamers[0]
	if streamer.Points != 110 || streamer.InitialPoints != 100 {
		t.Fatalf("unexpected final points state: %#v", streamer)
	}
	if got := streamer.History["WATCH"]; got.Count != 1 || got.Amount != 15 {
		t.Fatalf("watch history got %#v", got)
	}
	if got := streamer.History["BALANCE_CHANGE"]; got.Count != 1 || got.Amount != -5 {
		t.Fatalf("balance history got %#v", got)
	}
}

func TestKickWatchRecoveryKeepsInterruptedStreamSelected(t *testing.T) {
	runtime := newKickAccountRuntime(KickAccountConfig{
		Alias:         "Test",
		Token:         "token",
		Streamers:     []string{"High", "Interrupted"},
		MaxConcurrent: 1,
	}, KickSettings{}, NewLogger(LoggerSettings{}, ""))
	now := time.Now()
	runtime.streamers["high"].Online = true
	runtime.streamers["high"].StreamID = 1
	interrupted := runtime.streamers["interrupted"]
	interrupted.Online = true
	interrupted.StreamID = 2
	interrupted.RecoveryStreamID = 2
	interrupted.RecoveryUntil = now.Add(time.Minute)

	desired := runtime.desiredStreamersLocked(now)
	if _, ok := desired["interrupted"]; !ok {
		t.Fatalf("interrupted stream was not selected: %#v", desired)
	}
	if _, ok := desired["high"]; ok {
		t.Fatalf("normal priority replaced active recovery: %#v", desired)
	}

	desired = runtime.desiredStreamersLocked(now.Add(2 * time.Minute))
	if _, ok := desired["high"]; !ok {
		t.Fatalf("normal priority did not resume after recovery expired: %#v", desired)
	}
}

func TestKickPointGainCompletesWatchRecovery(t *testing.T) {
	runtime := newKickAccountRuntime(KickAccountConfig{
		Alias:         "Test",
		Token:         "token",
		Streamers:     []string{"Streamer"},
		MaxConcurrent: 1,
	}, KickSettings{}, NewLogger(LoggerSettings{}, ""))
	streamer := runtime.streamers["streamer"]
	streamer.StreamID = 7
	streamer.Points = 100
	streamer.HasPoints = true
	streamer.InitialPoints = 100
	streamer.InitialPointsSet = true
	streamer.RecoveryStreamID = 7
	streamer.RecoveryUntil = time.Now().Add(kickWatchRecovery)

	runtime.updateKickPoints("streamer", 110, "")
	if streamer.RecoveryStreamID != 0 || !streamer.RecoveryUntil.IsZero() {
		t.Fatalf("point gain did not complete recovery: %#v", streamer)
	}
}

func TestKickRuntimeUsesFollowedModeForEmptyStreamerList(t *testing.T) {
	runtime := newKickAccountRuntime(KickAccountConfig{
		Alias: "Test",
		Token: "token",
	}, KickSettings{}, NewLogger(LoggerSettings{}, ""))

	if !runtime.followed {
		t.Fatal("empty streamer list should use followed channel mode")
	}
	if len(runtime.streamerOrder()) != 0 {
		t.Fatalf("unexpected initial streamer order: %#v", runtime.streamerOrder())
	}
}

func TestKickRuntimeSyncsFollowedStreamers(t *testing.T) {
	runtime := newKickAccountRuntime(KickAccountConfig{
		Alias:     "Test",
		Token:     "token",
		Streamers: []string{"Old", "Kept"},
	}, KickSettings{}, NewLogger(LoggerSettings{}, ""))

	watchContext, cancel := context.WithCancel(context.Background())
	runtime.streamers["old"].Watching = true
	runtime.streamers["old"].CancelFunc = cancel

	added, removed := runtime.syncStreamers([]string{"Kept", "New", "kept"})
	if len(added) != 1 || added[0] != "new" {
		t.Fatalf("added got %#v", added)
	}
	if len(removed) != 1 || removed[0] != "old" {
		t.Fatalf("removed got %#v", removed)
	}
	select {
	case <-watchContext.Done():
	default:
		t.Fatal("removed streamer watcher was not canceled")
	}
	if _, exists := runtime.streamers["old"]; exists {
		t.Fatal("removed streamer remains in runtime")
	}
	order := runtime.streamerOrder()
	if len(order) != 2 || order[0] != "kept" || order[1] != "new" {
		t.Fatalf("order got %#v", order)
	}
	if runtime.streamers["kept"].Priority != 0 || runtime.streamers["new"].Priority != 1 {
		t.Fatalf("priorities not updated: kept=%d new=%d", runtime.streamers["kept"].Priority, runtime.streamers["new"].Priority)
	}
}

func TestKickStatusShowsUnknownUntilPointsLoaded(t *testing.T) {
	if got := formatKickPoints(0, false, false); got != "?" {
		t.Fatalf("unknown points got %q", got)
	}
	if got := formatKickPoints(2630, true, false); got != "2.63k" {
		t.Fatalf("known points got %q", got)
	}
}

func TestKickUpdateStreamerInfoClearsOfflineStreamID(t *testing.T) {
	runtime := newKickAccountRuntime(KickAccountConfig{
		Alias:     "Test",
		Token:     "token",
		Streamers: []string{"Streamer"},
	}, KickSettings{}, NewLogger(LoggerSettings{}, ""))

	runtime.updateStreamerInfo("streamer", kickChannelInfo{
		ChannelID: 1,
		UserID:    2,
		StreamID:  3,
		Online:    true,
	})
	streamer := runtime.snapshot().Streamers[0]
	if !streamer.Online {
		t.Fatalf("expected online streamer: %#v", streamer)
	}

	runtime.updateStreamerInfo("streamer", kickChannelInfo{
		ChannelID: 1,
		UserID:    2,
		Online:    false,
	})
	state := runtime.streamers["streamer"]
	if state.Online || state.StreamID != 0 {
		t.Fatalf("offline update should clear online state and stream id: %#v", state)
	}
}

func TestKickStartStreamerSkipsOfflineChannel(t *testing.T) {
	runtime := newKickAccountRuntime(KickAccountConfig{
		Alias:     "Test",
		Token:     "token",
		Streamers: []string{"Streamer"},
	}, KickSettings{}, NewLogger(LoggerSettings{}, ""))

	runtime.startStreamer(context.Background(), "streamer")

	if runtime.streamers["streamer"].Watching {
		t.Fatalf("offline streamer should not be marked watching")
	}
}
