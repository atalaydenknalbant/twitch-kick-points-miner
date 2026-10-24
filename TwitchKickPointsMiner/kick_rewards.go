package twitchchannelpointsminer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

const kickRewardProgressLogInterval = 5 * time.Minute

type kickRewardWinner struct {
	ID     string
	Name   string
	Rarity string
}

type kickRewardChallenge struct {
	ID             string
	Name           string
	Status         string
	WindowStartsAt string
	WindowEndsAt   string
	EventStartsAt  string
	EventEndsAt    string
	ConditionType  string
	Progress       int
	Target         int
	ProgressKnown  bool
	Winner         kickRewardWinner
}

type kickRewardState struct {
	Challenge   kickRewardChallenge
	LastLogged  kickRewardChallenge
	LastLogAt   time.Time
	ClaimFailed bool
}

type kickRewardsSnapshot struct {
	CheckedAt      time.Time
	Unavailable    bool
	ClaimedSession int
	Challenges     []kickRewardChallenge
}

func (r *kickAccountRuntime) checkRewards(ctx context.Context) {
	if !r.settings.claimDailyRewardsEnabled() || ctx.Err() != nil {
		return
	}
	rewards, err := r.client.rewardChallenges(ctx)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		r.mu.Lock()
		wasFailed := r.rewardCheckFailed
		r.rewardCheckFailed = true
		r.mu.Unlock()
		if !wasFailed {
			r.logger.Printf("%s Reward tracking temporarily unavailable; retrying automatically", constants.PlatformKickToken)
		}
		r.logger.Debugf("%s reward check failed: %v", constants.PlatformKickToken, err)
		return
	}
	now := time.Now()
	r.mu.Lock()
	wasFailed := r.rewardCheckFailed
	r.rewardCheckFailed = false
	r.rewardsCheckedAt = now
	states := make(map[string]kickRewardState, len(rewards))
	order := make([]string, 0, len(rewards))
	for _, reward := range rewards {
		key := reward.key()
		state := r.rewards[key]
		// Keep a successful claim even if a subsequent GET briefly returns stale data.
		if _, claimed := r.claimedRewards[key]; claimed {
			reward.Status = "claimed"
			if reward.Winner.ID == "" {
				reward.Winner = state.Challenge.Winner
			}
		}
		reward.Status = reward.statusAt(now)
		state.Challenge = reward
		states[key] = state
		order = append(order, key)
	}
	r.rewards = states
	r.rewardOrder = order
	r.mu.Unlock()
	if wasFailed {
		r.logger.Printf("%s Reward tracking resumed", constants.PlatformKickToken)
	}

	for _, key := range order {
		if ctx.Err() != nil {
			return
		}
		r.mu.Lock()
		state := r.rewards[key]
		r.mu.Unlock()
		reward := state.Challenge
		claimedNow := false
		if reward.statusAt(time.Now()) == "claimable" {
			winner, err := r.client.claimReward(ctx, reward.ID)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				if !state.ClaimFailed {
					r.logger.Printf("%s %s | Claim pending; retrying automatically", constants.PlatformKickToken, reward.label())
				}
				r.logger.Debugf("%s reward claim failed: %v", constants.PlatformKickToken, err)
				state.ClaimFailed = true
			} else {
				reward.Status = "claimed"
				reward.Winner = winner
				state.ClaimFailed = false
				claimedNow = true
			}
		}
		shouldLog := claimedNow || state.LastLogAt.IsZero() ||
			state.LastLogged.Status != reward.Status || state.LastLogged.Winner != reward.Winner ||
			state.LastLogged.Name != reward.Name ||
			(now.Sub(state.LastLogAt) >= kickRewardProgressLogInterval && state.LastLogged != reward)
		state.Challenge = reward
		if shouldLog {
			state.LastLogAt = now
			state.LastLogged = reward
		}
		r.mu.Lock()
		if reward.Status == "claimed" {
			if r.claimedRewards == nil {
				r.claimedRewards = make(map[string]struct{})
			}
			r.claimedRewards[key] = struct{}{}
		}
		if claimedNow {
			r.rewardsClaimed++
		}
		r.rewards[key] = state
		r.mu.Unlock()
		if shouldLog {
			if claimedNow {
				r.logger.EmojiPrintf(":gift:", "%s %s | Claimed!%s", constants.PlatformKickToken, reward.label(), reward.details())
			} else {
				r.logger.Printf("%s %s", constants.PlatformKickToken, reward.line(now))
			}
		}
	}
}

func (r *kickAccountRuntime) rewardSnapshotLocked() kickRewardsSnapshot {
	snapshot := kickRewardsSnapshot{
		CheckedAt: r.rewardsCheckedAt, Unavailable: r.rewardCheckFailed,
		ClaimedSession: r.rewardsClaimed,
		Challenges:     make([]kickRewardChallenge, 0, len(r.rewardOrder)),
	}
	for _, key := range r.rewardOrder {
		snapshot.Challenges = append(snapshot.Challenges, r.rewards[key].Challenge)
	}
	return snapshot
}

func (r kickRewardChallenge) key() string {
	return r.ID + "|" + r.WindowStartsAt + "|" + r.WindowEndsAt
}

func (r kickRewardChallenge) label() string {
	if name := cleanKickRewardText(r.Name); name != "" {
		return name
	}
	return "Reward"
}

func (r kickRewardChallenge) statusAt(now time.Time) string {
	if r.Status == "claimed" {
		return r.Status
	}
	for _, raw := range []string{r.WindowEndsAt, r.EventEndsAt} {
		if end, err := time.Parse(time.RFC3339Nano, raw); err == nil && !now.Before(end) {
			return "expired"
		}
	}
	for _, raw := range []string{r.WindowStartsAt, r.EventStartsAt} {
		if start, err := time.Parse(time.RFC3339Nano, raw); err == nil && now.Before(start) {
			return "upcoming"
		}
	}
	return r.Status
}

func (r kickRewardChallenge) line(now time.Time) string {
	status := r.statusAt(now)
	label := map[string]string{
		"claimed": "Claimed", "claimable": "Ready to claim", "in_progress": "In progress",
		"expired": "Expired", "upcoming": "Upcoming",
	}[status]
	if label == "" {
		label = "Waiting for eligibility"
	}
	line := r.label() + " | " + label + r.details()
	if status != "claimed" && status != "expired" {
		if end, err := time.Parse(time.RFC3339Nano, r.WindowEndsAt); err == nil {
			line += " | Claim before " + end.Local().Format("Jan 2 15:04 MST")
		}
	}
	if end, err := time.Parse(time.RFC3339Nano, r.EventEndsAt); err == nil {
		line += " | Event ends " + end.Local().Format("Jan 2 15:04 MST")
	}
	return line
}

func (r kickRewardChallenge) details() string {
	details := ""
	// This is Kick's credited progress, not a local timer or summed channel watch time.
	if r.ProgressKnown && r.ConditionType == "watch_time_minutes" {
		progress := min(r.Progress, r.Target)
		percent := int(100 * float64(progress) / float64(r.Target))
		details = fmt.Sprintf(" | Watch %d/%d min (%d%%)", progress, r.Target, percent)
		if r.Status != "claimed" && progress < r.Target {
			details += fmt.Sprintf(" | %d min remaining", r.Target-progress)
		}
	}
	if r.Status == "claimed" {
		name := cleanKickRewardText(r.Winner.Name)
		rarity := cleanKickRewardText(r.Winner.Rarity)
		if name != "" {
			details += " | Reward: " + name
		}
		if rarity != "" {
			details += " | Rarity: " + rarity
		}
	}
	return details
}

func cleanKickRewardText(text string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)), " ")
}

func parseKickRewards(payload map[string]interface{}) ([]kickRewardChallenge, error) {
	rawRewards, ok := payload["data"].([]interface{})
	if !ok {
		return nil, errors.New("rewards response missing data")
	}
	rewards := make([]kickRewardChallenge, 0, len(rawRewards))
	seen := make(map[string]struct{}, len(rawRewards))
	for _, raw := range rawRewards {
		data, ok := raw.(map[string]interface{})
		if !ok {
			return nil, errors.New("invalid reward challenge")
		}
		reward := kickRewardChallenge{
			ID: kickValueString(data["id"]), Name: kickValueString(data["name"]),
			Status: strings.ToLower(kickValueString(data["status"])),
			Winner: parseKickRewardWinner(data["winner"]),
		}
		if reward.ID == "" {
			return nil, errors.New("reward challenge id missing")
		}
		if window, ok := data["window"].(map[string]interface{}); ok {
			reward.WindowStartsAt = kickValueString(window["starts_at"])
			reward.WindowEndsAt = kickValueString(window["ends_at"])
		}
		if period, ok := data["active_period"].(map[string]interface{}); ok {
			reward.EventStartsAt = kickValueString(period["starts_at"])
			reward.EventEndsAt = kickValueString(period["ends_at"])
		}
		for _, rawTime := range []string{reward.WindowStartsAt, reward.WindowEndsAt, reward.EventStartsAt, reward.EventEndsAt} {
			if rawTime != "" {
				if _, err := time.Parse(time.RFC3339Nano, rawTime); err != nil {
					return nil, errors.New("invalid reward availability time")
				}
			}
		}
		if condition, ok := data["condition"].(map[string]interface{}); ok {
			reward.ConditionType = kickValueString(condition["type"])
			progress, hasProgress := toInt(condition["progress"])
			target, hasTarget := toInt(condition["threshold"])
			if hasProgress && hasTarget && progress >= 0 && target > 0 {
				reward.Progress, reward.Target, reward.ProgressKnown = progress, target, true
			}
		}
		if _, exists := seen[reward.key()]; exists {
			continue
		}
		seen[reward.key()] = struct{}{}
		rewards = append(rewards, reward)
	}
	return rewards, nil
}

func parseKickRewardWinner(raw interface{}) kickRewardWinner {
	data, _ := raw.(map[string]interface{})
	return kickRewardWinner{
		ID: kickValueString(data["id"]), Name: kickValueString(data["name"]),
		Rarity: kickValueString(data["rarity"]),
	}
}

func parseKickRewardClaim(body map[string]interface{}) (kickRewardWinner, error) {
	data := unwrapKickData(body)
	for _, value := range []map[string]interface{}{body, data} {
		if message := kickValueString(value["error"]); message != "" {
			return kickRewardWinner{}, errors.New("reward claim was not confirmed")
		}
		for _, field := range []string{"success", "claimed"} {
			if accepted, ok := value[field].(bool); ok && !accepted {
				return kickRewardWinner{}, errors.New("reward claim was not accepted")
			}
		}
		if status := strings.ToLower(kickValueString(value["status"])); status != "" && status != "claimed" && status != "success" {
			return kickRewardWinner{}, errors.New("reward claim was not confirmed")
		}
	}
	return parseKickRewardWinner(data["winner"]), nil
}

func (m *KickMiner) logRewardSummary(now time.Time, snapshots []kickAccountSnapshot) {
	if m.logger == nil {
		return
	}
	claimedSession, claimedWindow, ready, progressing := 0, 0, 0, 0
	checked, unavailable := false, false
	for _, account := range snapshots {
		snapshot := account.Rewards
		checked = checked || !snapshot.CheckedAt.IsZero()
		unavailable = unavailable || snapshot.Unavailable
		claimedSession += snapshot.ClaimedSession
		for _, challenge := range snapshot.Challenges {
			switch challenge.statusAt(now) {
			case "claimed":
				if end, err := time.Parse(time.RFC3339Nano, challenge.WindowEndsAt); err != nil || now.Before(end) {
					claimedWindow++
				}
			case "claimable":
				ready++
			case "in_progress":
				progressing++
			}
		}
	}
	if !checked && !unavailable {
		return
	}
	extra := ""
	if unavailable {
		extra = " | Refresh pending"
	}
	m.logger.Printf("%s Rewards | %d claimed this session | %d claimed this window | %d ready | %d in progress%s",
		constants.PlatformKickToken, claimedSession, claimedWindow, ready, progressing, extra)
	for _, account := range snapshots {
		for _, challenge := range account.Rewards.Challenges {
			if status := challenge.statusAt(now); status == "claimed" || status == "expired" {
				continue
			}
			extra := ""
			if account.Rewards.Unavailable {
				extra = " | Last checked " + account.Rewards.CheckedAt.Local().Format("15:04 MST")
			}
			m.logger.Printf("%s %s%s", constants.PlatformKickToken, challenge.line(now), extra)
		}
	}
}
