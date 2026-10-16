package classes

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/classes/entities"
	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

// Playback requests follow upstream commit d7d64c3, including every listed segment.
func (t *Twitch) sendWatchPlaylist(streamer *entities.Streamer) error {
	masterURL := ""
	if streamer.Stream.PlaylistURL == "" {
		op := constants.ClonePersistedOperation(constants.GQLOperations.PlaybackAccessToken)
		op.Variables = map[string]interface{}{
			"isLive": true, "isVod": false, "login": streamer.Username,
			"platform": "web", "playerType": "site", "vodID": "",
		}
		var response struct {
			Data struct {
				Token *struct {
					Value     string `json:"value"`
					Signature string `json:"signature"`
				} `json:"streamPlaybackAccessToken"`
			} `json:"data"`
		}
		if err := t.PostGQLDecode(op, &response); err != nil {
			return err
		}
		token := response.Data.Token
		if token == nil || token.Value == "" || token.Signature == "" {
			return errors.New("stream playback token unavailable")
		}
		masterURL = "https://usher.ttvnw.net/api/channel/hls/" + url.PathEscape(streamer.Username) + ".m3u8?" +
			url.Values{"sig": {token.Signature}, "token": {token.Value}}.Encode()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if masterURL != "" {
		variants, err := t.readStreamPlaylist(ctx, masterURL)
		if err != nil {
			return err
		}
		selected := variants[len(variants)-1]
		if !strings.HasSuffix(selected.Path, ".m3u8") {
			return errors.New("invalid stream master playlist")
		}
		streamer.Stream.PlaylistURL = selected.String()
	}
	segments, err := t.readStreamPlaylist(ctx, streamer.Stream.PlaylistURL)
	if err != nil {
		streamer.Stream.PlaylistURL = ""
		return err
	}
	for _, segment := range segments {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, segment.String(), nil)
		if err != nil {
			return errors.New("invalid stream segment URL")
		}
		req.Header.Set("User-Agent", t.userAgent)
		resp, err := t.client.Do(req)
		if err != nil {
			streamer.Stream.PlaylistURL = ""
			return errors.New("stream segment request failed")
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			streamer.Stream.PlaylistURL = ""
			return fmt.Errorf("stream segment returned status %d", resp.StatusCode)
		}
	}
	return nil
}

func (t *Twitch) readStreamPlaylist(ctx context.Context, address string) ([]*url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("invalid stream playlist URL")
	}
	req.Header.Set("User-Agent", t.userAgent)
	req.Close = true
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, errors.New("stream playlist request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stream playlist returned status %d", resp.StatusCode)
	}
	baseURL := req.URL
	if resp.Request != nil && resp.Request.URL != nil {
		baseURL = resp.Request.URL
	}
	scanner := bufio.NewScanner(resp.Body)
	var entries []*url.URL
	header := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !header {
			if line != "#EXTM3U" {
				return nil, errors.New("invalid stream playlist")
			}
			header = true
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		entry, err := url.Parse(line)
		if err != nil {
			return nil, errors.New("invalid stream playlist entry")
		}
		entry = baseURL.ResolveReference(entry)
		if (entry.Scheme != "http" && entry.Scheme != "https") || entry.Host == "" || entry.User != nil {
			return nil, errors.New("invalid stream playlist entry")
		}
		entries = append(entries, entry)
	}
	if scanner.Err() != nil || len(entries) == 0 {
		return nil, errors.New("unreadable or empty stream playlist")
	}
	return entries, nil
}

func (t *Twitch) updatePlaybackStatus(streamer *entities.Streamer, playbackErr error) {
	previous := streamer.Stream.PlaybackError
	current := ""
	if playbackErr != nil {
		current = playbackErr.Error()
	}
	streamer.Stream.PlaybackError = current
	if t.logger == nil || previous == current || !streamer.Settings.ClaimDrops {
		return
	}
	name := streamer.Username
	if t.anonymizer != nil {
		name = t.anonymizer.StreamerName(streamer)
	}
	if current != "" {
		t.logger.Printf("%s Drops playback for %s: %s; retrying automatically", constants.PlatformTwitchToken, name, current)
		return
	}
	t.logger.Printf("%s Drops playback requests recovered for %s", constants.PlatformTwitchToken, name)
}
