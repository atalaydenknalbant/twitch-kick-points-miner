package twitchchannelpointsminer

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

const (
	discordUsername = "Twitch Kick Points Miner"
)

type DiscordSettings struct {
	WebhookAPI string   `json:"webhook_api"`
	Events     []string `json:"events"`
}

type DiscordWebhook struct {
	webhookAPI string
	events     map[constants.Event]struct{}
	client     *http.Client
}

func NewDiscordWebhook(settings DiscordSettings) *DiscordWebhook {
	webhookAPI := strings.TrimSpace(settings.WebhookAPI)
	if webhookAPI == "" {
		return nil
	}
	events := make(map[constants.Event]struct{})
	for _, raw := range settings.Events {
		event := constants.NormalizeEventName(raw)
		if event == "" {
			continue
		}
		events[event] = struct{}{}
	}
	return &DiscordWebhook{
		webhookAPI: webhookAPI,
		events:     events,
		client:     &http.Client{Timeout: 5 * time.Second},
	}
}

func (d *DiscordWebhook) Send(message string, event constants.Event) {
	if d == nil {
		return
	}
	if len(d.events) > 0 {
		if _, ok := d.events[event]; !ok {
			return
		}
	} else if event == "" {
		return
	}
	payload := url.Values{
		"content":  []string{message},
		"username": []string{discordUsername},
	}
	req, err := http.NewRequest(http.MethodPost, d.webhookAPI, strings.NewReader(payload.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := d.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}
