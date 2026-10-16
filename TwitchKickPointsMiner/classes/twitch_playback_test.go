package classes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/privacy"
)

type segmentHeadBody struct {
	read, closed bool
}

func (b *segmentHeadBody) Read([]byte) (int, error) {
	b.read = true
	return 0, io.EOF
}

func (b *segmentHeadBody) Close() error {
	b.closed = true
	return nil
}

func TestSendMinuteWatchedRequestsEverySegmentAndRefreshesFailures(t *testing.T) {
	for _, failure := range []string{"none", "HTTP error", "transport error", "playlist error"} {
		t.Run(failure, func(t *testing.T) {
			var heads []string
			var bodies []*segmentHeadBody
			tokenCalls, masterCalls, playlistCalls, telemetryCalls := 0, 0, 0, 0
			fail := failure != "none"
			twitch := &Twitch{
				userAgent:      "fixture",
				clientVersion:  constants.ClientVersion,
				versionFetched: time.Now(),
				versionTTL:     time.Hour,
				twitchLogin:    &TwitchLogin{Token: "token"},
				client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					if req.URL.Host != "gql.twitch.tv" && req.Header.Get("Authorization") != "" {
						t.Fatal("Twitch authorization was sent to a playback host")
					}
					switch {
					case req.URL.Host == "gql.twitch.tv":
						tokenCalls++
						var operation constants.GQLPersistedOperation
						if err := json.NewDecoder(req.Body).Decode(&operation); err != nil {
							t.Fatal(err)
						}
						if operation.OperationName != "PlaybackAccessToken" ||
							operation.Extensions.PersistedQuery.Sha256Hash != "ed230aa1e33e07eebb8928504583da78a5173989fadfb1ac94be06a04f3cdbe9" {
							t.Fatalf("incorrect playback token operation: %+v", operation)
						}
						if operation.Variables["login"] != "streamer" || operation.Variables["isLive"] != true {
							t.Fatalf("incorrect playback token variables: %+v", operation.Variables)
						}
						return jsonResponse(200, "{\"data\":{\"streamPlaybackAccessToken\":{\"value\":\"token+&=\",\"signature\":\"signature\"}}}"), nil
					case req.URL.Host == "usher.ttvnw.net":
						masterCalls++
						if req.Method != http.MethodGet || req.URL.Query().Get("token") != "token+&=" || req.URL.Query().Get("sig") != "signature" {
							t.Fatal("playback token query was not encoded correctly")
						}
						return jsonResponse(200, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\nhttps://video.example/high.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=64000\nhttps://video.example/media/playlist.m3u8\n"), nil
					case req.Method == http.MethodGet && req.URL.Path == "/media/playlist.m3u8":
						playlistCalls++
						if fail && failure == "playlist error" {
							return jsonResponse(http.StatusForbidden, "expired"), nil
						}
						return jsonResponse(200, "#EXTM3U\n#EXTINF:2.0,\none.ts?part=1\n#EXTINF:2.0,\n../two.ts\n#EXTINF:2.0,\nhttps://cdn.example/three.ts\n"), nil
					case req.Method == http.MethodHead:
						heads = append(heads, req.URL.String())
						if _, ok := req.Context().Deadline(); !ok {
							t.Fatal("segment request has no deadline")
						}
						if req.Header.Get("User-Agent") != "fixture" {
							t.Fatal("segment request has no viewer user agent")
						}
						if fail && failure == "transport error" {
							return nil, errors.New("fixture connection failed")
						}
						body := &segmentHeadBody{}
						bodies = append(bodies, body)
						status := http.StatusOK
						if fail {
							status = http.StatusServiceUnavailable
						}
						return &http.Response{StatusCode: status, Header: make(http.Header), Body: body}, nil
					case req.Method == http.MethodPost && req.URL.Host == "spade.example":
						telemetryCalls++
						return jsonResponse(http.StatusNoContent, ""), nil
					default:
						t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
						return nil, errors.New("unexpected request")
					}
				})},
			}
			streamer := newTestStreamer(false)
			streamer.Stream.Update("broadcast", "title", nil, nil, 0, time.Time{}, "")
			streamer.Stream.SpadeURL = "https://spade.example/track"
			wantSegments := []string{
				"https://video.example/media/one.ts?part=1",
				"https://video.example/two.ts",
				"https://cdn.example/three.ts",
			}

			err := twitch.SendMinuteWatched(streamer)
			if err != nil {
				t.Fatalf("watch error got %v for failure mode %s", err, failure)
			}
			if fail {
				if streamer.Stream.PlaylistURL != "" || streamer.Stream.PlaybackError == "" || telemetryCalls != 1 {
					t.Fatal("failed playback retained its cache or prevented channel points telemetry")
				}
				wantHeads := wantSegments[:1]
				if failure == "playlist error" {
					wantHeads = nil
				}
				if !slices.Equal(heads, wantHeads) {
					t.Fatalf("failed segment requests got %v, want %v", heads, wantHeads)
				}
			} else if !slices.Equal(heads, wantSegments) || telemetryCalls != 1 {
				t.Fatalf("playback did not request every segment before telemetry: %v, telemetry=%d", heads, telemetryCalls)
			}

			fail = false
			heads = nil
			if err := twitch.SendMinuteWatched(streamer); err != nil {
				t.Fatalf("next watch attempt failed: %v", err)
			}
			wantTokenCalls := 1
			if failure != "none" {
				wantTokenCalls = 2
			}
			if tokenCalls != wantTokenCalls || masterCalls != wantTokenCalls || playlistCalls != 2 || telemetryCalls != 2 {
				t.Fatalf("incorrect cache behavior: tokens=%d masters=%d playlists=%d telemetry=%d", tokenCalls, masterCalls, playlistCalls, telemetryCalls)
			}
			if !slices.Equal(heads, wantSegments) || streamer.Stream.PlaylistURL != "https://video.example/media/playlist.m3u8" {
				t.Fatalf("next playback attempt missed segments or did not cache the playlist: %v", heads)
			}
			if streamer.Stream.PlaybackError != "" {
				t.Fatal("recovered playback kept an obsolete failure status")
			}
			for _, body := range bodies {
				if body.read || !body.closed {
					t.Fatal("segment media data was read or the response was not closed")
				}
			}
		})
	}
}

type playbackStatusLogger struct {
	stubPubSubLogger
	messages []string
}

func (l *playbackStatusLogger) Printf(format string, args ...interface{}) {
	l.messages = append(l.messages, fmt.Sprintf(format, args...))
}

func TestPlaybackStatusLogsTransitionsAndHonorsAnonymization(t *testing.T) {
	logger := &playbackStatusLogger{}
	twitch := &Twitch{logger: logger, anonymizer: privacy.New(true)}
	streamer := newTestStreamer(false)
	streamer.Settings.ClaimDrops = true
	failure := errors.New("stream playlist returned status 403")
	twitch.updatePlaybackStatus(streamer, failure)
	twitch.updatePlaybackStatus(streamer, failure)
	twitch.updatePlaybackStatus(streamer, nil)
	twitch.updatePlaybackStatus(streamer, nil)
	if len(logger.messages) != 2 {
		t.Fatalf("expected a failure and recovery notice without repeats: %v", logger.messages)
	}
	logs := strings.Join(logger.messages, "\n")
	if strings.Contains(logs, streamer.Username) || !strings.Contains(logs, "Streamer1") || !strings.Contains(logs, "recovered") {
		t.Fatalf("playback status lost privacy or recovery information: %s", logs)
	}
	streamer.Settings.ClaimDrops = false
	twitch.updatePlaybackStatus(streamer, failure)
	if len(logger.messages) != 2 {
		t.Fatal("disabled Drops emitted an unnecessary playback notice")
	}
}

func TestSendMinuteWatchedStillReportsTelemetryFailures(t *testing.T) {
	twitch := &Twitch{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case http.MethodGet:
			return jsonResponse(200, "#EXTM3U\nsegment.ts\n"), nil
		case http.MethodHead:
			return jsonResponse(200, ""), nil
		default:
			return jsonResponse(http.StatusServiceUnavailable, "unavailable"), nil
		}
	})}}
	streamer := newTestStreamer(false)
	streamer.Stream.Update("broadcast", "title", nil, nil, 0, time.Time{}, "")
	streamer.Stream.PlaylistURL = "https://video.example/live.m3u8"
	streamer.Stream.SpadeURL = "https://spade.example/track"
	if err := twitch.SendMinuteWatched(streamer); err == nil {
		t.Fatal("failed channel points telemetry was reported as successful")
	}
}

func TestReadStreamPlaylistRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name, body string
		status     int
	}{
		{name: "HTTP failure", status: http.StatusForbidden, body: "expired"},
		{name: "HTML response", status: http.StatusOK, body: "<html>error</html>"},
		{name: "empty playlist", status: http.StatusOK, body: "#EXTM3U\n#EXT-X-ENDLIST\n"},
		{name: "invalid URL", status: http.StatusOK, body: "#EXTM3U\n%zz\n"},
		{name: "invalid scheme", status: http.StatusOK, body: "#EXTM3U\nfile:///tmp/segment.ts\n"},
		{name: "embedded credentials", status: http.StatusOK, body: "#EXTM3U\nhttps://user:password@video.example/segment.ts\n"},
		{name: "oversized line", status: http.StatusOK, body: "#EXTM3U\n" + strings.Repeat("a", 70*1024)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			twitch := &Twitch{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return jsonResponse(test.status, test.body), nil
			})}}
			if _, err := twitch.readStreamPlaylist(context.Background(), "https://video.example/playlist.m3u8"); err == nil {
				t.Fatal("invalid playlist was accepted")
			}
		})
	}
}

func TestReadStreamPlaylistResolvesRelativeURLsAfterRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/old.m3u8" {
			http.Redirect(w, req, "/live/current.m3u8", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:2,\nsegment.ts\n")
	}))
	defer server.Close()
	twitch := &Twitch{client: server.Client()}
	entries, err := twitch.readStreamPlaylist(context.Background(), server.URL+"/old.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].String() != server.URL+"/live/segment.ts" {
		t.Fatalf("relative segment did not follow the final playlist URL: %v", entries)
	}
}

func TestSendWatchPlaylistRejectsMissingPlaybackToken(t *testing.T) {
	twitch := &Twitch{
		clientVersion: constants.ClientVersion, versionFetched: time.Now(), versionTTL: time.Hour,
		twitchLogin: &TwitchLogin{Token: "token"},
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host != "gql.twitch.tv" {
				t.Fatal("requested playback without a token")
			}
			return jsonResponse(200, "{\"data\":{\"streamPlaybackAccessToken\":null}}"), nil
		})},
	}
	if err := twitch.sendWatchPlaylist(newTestStreamer(false)); err == nil {
		t.Fatal("missing playback token was accepted")
	}
}
