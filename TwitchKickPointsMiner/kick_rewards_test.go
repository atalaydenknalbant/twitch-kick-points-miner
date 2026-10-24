package twitchchannelpointsminer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Field names and units match the challenges response observed on 2026-10-24.
const kickRewardsFixture = `{"data":[
	{"id":"daily","name":"Daily Reward","recurrence":"daily","status":"claimed",
	 "condition":{"progress":60,"threshold":60,"type":"watch_time_minutes"},
	 "window":{"starts_at":"2026-10-24T00:00:00Z","ends_at":"2026-10-25T00:00:00Z"},
	 "winner":{"id":"collectible","rarity":"common","card_url":"https://example.com/card.png"}},
	{"id":"halloween","name":"Trick or Treat","recurrence":"daily","status":"in_progress","featured":true,
	 "condition":{"progress":108,"threshold":180,"type":"watch_time_minutes"},
	 "window":{"starts_at":"2026-10-24T00:00:00Z","ends_at":"2026-10-25T00:00:00Z"},
	 "active_period":{"starts_at":"2026-10-24T00:00:00Z","ends_at":"2026-11-01T00:00:00Z"}}
]}`

func decodeKickRewards(t *testing.T, raw string) []kickRewardChallenge {
	t.Helper()
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	rewards, err := parseKickRewards(payload)
	if err != nil {
		t.Fatal(err)
	}
	return rewards
}

func newKickRewardTestRuntime(t *testing.T, transport kickRoundTripFunc) (*kickAccountRuntime, *bytes.Buffer) {
	t.Helper()
	var output bytes.Buffer
	logger := NewLogger(LoggerSettings{}, "")
	logger.base.SetOutput(&output)
	runtime := newKickAccountRuntime(KickAccountConfig{Token: "test-token"}, KickSettings{}, logger)
	runtime.client.initialized = true
	runtime.client.httpClient.Transport = transport
	return runtime, &output
}

func TestKickRewardsParseServerProgressAndEventWindow(t *testing.T) {
	rewards := decodeKickRewards(t, kickRewardsFixture)
	if len(rewards) != 2 || rewards[0].Winner.Rarity != "common" || rewards[1].Name != "Trick or Treat" {
		t.Fatalf("unexpected rewards: %#v", rewards)
	}
	event := rewards[1]
	if !event.ProgressKnown || event.Progress != 108 || event.Target != 180 || event.EventEndsAt != "2026-11-01T00:00:00Z" {
		t.Fatalf("event progress or deadline missing: %#v", event)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	line := event.line(now)
	for _, want := range []string{"Trick or Treat | In progress", "Watch 108/180 min (60%)", "72 min remaining", "Claim before", "Event ends"} {
		if !strings.Contains(line, want) {
			t.Fatalf("missing %q in %q", want, line)
		}
	}
	if !strings.Contains(rewards[0].line(now), "Claimed | Watch 60/60 min (100%) | Rarity: common") {
		t.Fatal("already claimed reward was not reported")
	}
}

func TestKickRewardsClaimDailyAndEventIndependentlyAndResetByWindow(t *testing.T) {
	windowEnd := time.Now().Add(24 * time.Hour).UTC()
	posts := map[string]int{}
	runtime, output := newKickRewardTestRuntime(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return kickTestResponse(req, http.StatusOK, fmt.Sprintf(`{"data":[
			{"id":"daily","name":"Daily Reward","status":"claimable","window":{"ends_at":%q}},
			{"id":"event","name":"Trick or Treat","status":"claimable","window":{"ends_at":%q}}
			]}`, windowEnd.Format(time.RFC3339Nano), windowEnd.Format(time.RFC3339Nano))), nil
		}
		posts[req.URL.Path]++
		return kickTestResponse(req, http.StatusOK, `{"data":{"claimed":true,"winner":{"id":"prize","name":"Reward name","rarity":"rare"}}}`), nil
	})
	for i := 0; i < 3; i++ {
		runtime.checkRewards(context.Background())
	}
	if len(posts) != 2 {
		t.Fatalf("expected separate challenge claims: %v", posts)
	}
	for path, count := range posts {
		if count != 1 {
			t.Fatalf("duplicate claim for %s: %d", path, count)
		}
	}
	snapshot := runtime.snapshot().Rewards
	if snapshot.ClaimedSession != 2 || snapshot.Challenges[1].Status != "claimed" || snapshot.Challenges[1].Winner.Rarity != "rare" {
		t.Fatalf("successful claims were not retained over stale GETs: %#v", snapshot)
	}
	if !strings.Contains(output.String(), "Trick or Treat | Claimed! | Reward: Reward name | Rarity: rare") {
		t.Fatalf("event claim details missing: %s", output.String())
	}
	windowEnd = windowEnd.Add(24 * time.Hour)
	runtime.checkRewards(context.Background())
	if runtime.snapshot().Rewards.ClaimedSession != 4 {
		t.Fatal("new windows did not become claimable independently")
	}
	for path, count := range posts {
		if count != 2 {
			t.Fatalf("next window claim for %s: %d", path, count)
		}
	}
}

func TestKickRewardsNeverInferClaimabilityFromWatchTime(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	runtime, _ := newKickRewardTestRuntime(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("ineligible challenge was claimed: %s", req.URL.Path)
		}
		return kickTestResponse(req, http.StatusOK, fmt.Sprintf(`{"data":[
		{"id":"waiting","status":"in_progress","condition":{"type":"watch_time_minutes","progress":180,"threshold":180}},
		{"id":"expired","status":"claimable","window":{"ends_at":%q}},
		{"id":"event-ended","status":"claimable","active_period":{"ends_at":%q}},
		{"id":"upcoming","status":"claimable","window":{"starts_at":%q}},
		{"id":"unknown","status":"unrecognized"}
		]}`, past, past, future)), nil
	})
	runtime.checkRewards(context.Background())
	if runtime.snapshot().Rewards.ClaimedSession != 0 {
		t.Fatal("ineligible rewards were counted as claimed")
	}
}

func TestKickRewardClaimFailureRetriesWithoutBlockingOtherChallenges(t *testing.T) {
	posts := map[string]int{}
	runtime, output := newKickRewardTestRuntime(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return kickTestResponse(req, http.StatusOK, `{"data":[
			{"id":"daily","name":"Daily Reward","status":"claimable"},
			{"id":"event","name":"Trick or Treat","status":"claimable"}]}`), nil
		}
		posts[req.URL.Path]++
		if strings.Contains(req.URL.Path, "/daily/") && posts[req.URL.Path] <= 2 {
			return kickTestResponse(req, http.StatusServiceUnavailable, `{"error":"temporary failure"}`), nil
		}
		return kickTestResponse(req, http.StatusNoContent, ""), nil
	})
	runtime.checkRewards(context.Background())
	if runtime.snapshot().Rewards.ClaimedSession != 1 {
		t.Fatal("daily failure prevented the event claim")
	}
	runtime.checkRewards(context.Background())
	runtime.checkRewards(context.Background())
	if runtime.snapshot().Rewards.ClaimedSession != 2 || posts["/api/v1/gamification/challenges/event/claim"] != 1 {
		t.Fatal("failed claim was not retried or successful claim was repeated")
	}
	if strings.Count(output.String(), "Claim pending;") != 1 || strings.Contains(output.String(), "503") {
		t.Fatalf("claim retry spammed normal logs: %s", output.String())
	}
}

func TestKickRewardsReportProgressWithoutRepeatingEveryPoll(t *testing.T) {
	progress, status := 108, "in_progress"
	runtime, output := newKickRewardTestRuntime(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			return kickTestResponse(req, http.StatusOK, `{"data":{"claimed":true}}`), nil
		}
		return kickTestResponse(req, http.StatusOK, fmt.Sprintf(`{"data":[{"id":"event","name":"Trick or Treat","status":%q,
		"condition":{"type":"watch_time_minutes","progress":%d,"threshold":180}}]}`, status, progress)), nil
	})
	runtime.checkRewards(context.Background())
	output.Reset()
	progress = 110
	runtime.checkRewards(context.Background())
	if output.Len() != 0 || runtime.snapshot().Rewards.Challenges[0].Progress != 110 {
		t.Fatalf("progress was not updated quietly: %s", output.String())
	}
	for key, state := range runtime.rewards {
		state.LastLogAt = time.Now().Add(-6 * time.Minute)
		runtime.rewards[key] = state
	}
	runtime.checkRewards(context.Background())
	if !strings.Contains(output.String(), "Watch 110/180 min") {
		t.Fatal("progress did not appear after the reporting interval")
	}
	output.Reset()
	status, progress = "claimable", 180
	runtime.checkRewards(context.Background())
	if !strings.Contains(output.String(), "Trick or Treat | Claimed!") {
		t.Fatal("claim transition was hidden by progress throttling")
	}
}

func TestKickRewardSummaryIncludesServerClaimsAndStaleProgress(t *testing.T) {
	fail := false
	payload := strings.ReplaceAll(kickRewardsFixture, "2026-10-25", "2099-10-25")
	payload = strings.ReplaceAll(payload, "2026-11-01", "2099-11-01")
	payload = strings.ReplaceAll(payload, "2026-10-24", "2000-10-24")
	runtime, output := newKickRewardTestRuntime(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatal("already claimed or incomplete reward should not be posted")
		}
		if fail {
			return kickTestResponse(req, http.StatusBadGateway, "<html>Bad Gateway</html>"), nil
		}
		return kickTestResponse(req, http.StatusOK, payload), nil
	})
	runtime.checkRewards(context.Background())
	fail = true
	runtime.checkRewards(context.Background())
	runtime.checkRewards(context.Background())
	if strings.Count(output.String(), "temporarily unavailable") != 1 || strings.Contains(output.String(), "502") {
		t.Fatalf("reward failures spammed normal logs: %s", output.String())
	}
	output.Reset()
	miner := &Miner{logger: runtime.logger, kickMiner: &KickMiner{logger: runtime.logger, runtimes: []*kickAccountRuntime{runtime}}}
	miner.logSessionSummary(time.Now())
	for _, want := range []string{"0 claimed this session | 1 claimed this window | 0 ready | 1 in progress", "Refresh pending", "Last checked", "Watch 108/180 min"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in summary: %s", want, output.String())
		}
	}
	fail = false
	runtime.checkRewards(context.Background())
	if !strings.Contains(output.String(), "Reward tracking resumed") || runtime.snapshot().Rewards.Unavailable {
		t.Fatal("tracking did not recover")
	}
	payload = `{"data":[]}`
	runtime.checkRewards(context.Background())
	if len(runtime.snapshot().Rewards.Challenges) != 0 {
		t.Fatal("removed event remained in current reward tracking")
	}
}

func TestKickRewardRejectsInvalidSchemasAndUnconfirmedClaims(t *testing.T) {
	for _, raw := range []string{`{}`, `{"data":{}}`, `{"data":[null]}`, `{"data":[{"status":"claimable"}]}`, `{"data":[{"id":"x","window":{"ends_at":"invalid"}}]}`} {
		var body map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatal(err)
		}
		if _, err := parseKickRewards(body); err == nil {
			t.Fatalf("accepted invalid challenges: %s", raw)
		}
	}
	for _, raw := range []string{`{"success":false}`, `{"data":{"claimed":false}}`, `{"data":{"status":"failed"}}`, `{"data":{"status":"in_progress"}}`, `{"error":"OK"}`} {
		var body map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatal(err)
		}
		if _, err := parseKickRewardClaim(body); err == nil {
			t.Fatalf("claim failure reported as success: %s", raw)
		}
	}
	for _, raw := range []string{
		`{"data":[{"id":"x","condition":{"type":"watch_time_minutes","progress":10,"threshold":0}}]}`,
		`{"data":[{"id":"x","condition":{"type":"watch_time_minutes","progress":-5,"threshold":60}}]}`,
		`{"data":[{"id":"x","condition":{"type":"messages_sent","progress":10,"threshold":60}}]}`,
	} {
		if text := decodeKickRewards(t, raw)[0].details(); strings.Contains(text, "Watch") {
			t.Fatalf("invented watch minutes from invalid or unrelated condition: %s", text)
		}
	}
}

func TestKickRewardCanceledCheckDoesNotRequestOrClaim(t *testing.T) {
	runtime, _ := newKickRewardTestRuntime(t, func(req *http.Request) (*http.Response, error) {
		t.Fatal("canceled reward check made a request")
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runtime.checkRewards(ctx)
}
