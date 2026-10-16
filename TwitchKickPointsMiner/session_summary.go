package twitchchannelpointsminer

import (
	"time"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/classes/entities"
	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

type sessionPointBalance struct {
	initial, current int
}

func (m *Miner) SetSessionSummaryInterval(interval time.Duration) {
	m.sessionSummaryInterval = max(interval, 0)
}

func (m *Miner) recordSummaryPresence(streamer *entities.Streamer, online bool) {
	m.summaryMu.Lock()
	defer m.summaryMu.Unlock()
	if m.summaryOnline == nil {
		m.summaryOnline = make(map[*entities.Streamer]bool)
	}
	m.summaryOnline[streamer] = online
	if !online {
		delete(m.summaryWatching, streamer)
	}
}

func (m *Miner) recordSummaryWatching(streamers []*entities.Streamer) {
	m.summaryMu.Lock()
	defer m.summaryMu.Unlock()
	m.summaryWatching = make(map[*entities.Streamer]struct{}, len(streamers))
	for _, streamer := range streamers {
		if m.summaryOnline[streamer] {
			m.summaryWatching[streamer] = struct{}{}
		}
	}
}

func (m *Miner) recordSummaryBalance(streamer *entities.Streamer, previous, current int) {
	m.summaryMu.Lock()
	defer m.summaryMu.Unlock()
	if m.summaryPoints == nil {
		m.summaryPoints = make(map[*entities.Streamer]sessionPointBalance)
	}
	points, exists := m.summaryPoints[streamer]
	if !exists {
		points.initial = previous
	}
	points.current = current
	m.summaryPoints[streamer] = points
}

func (m *Miner) logSessionSummary(now time.Time) {
	if m.logger == nil {
		return
	}
	elapsed := time.Duration(0)
	if !m.startedAt.IsZero() {
		elapsed = max(now.Sub(m.startedAt), 0)
	}
	m.logger.Printf("Session summary | Uptime %s", formatDuration(elapsed))
	if m.TwitchEnabled {
		m.summaryMu.Lock()
		netPoints := 0
		for _, points := range m.summaryPoints {
			netPoints += points.current - points.initial
		}
		online := 0
		for _, live := range m.summaryOnline {
			if live {
				online++
			}
		}
		watching := len(m.summaryWatching)
		m.summaryMu.Unlock()
		m.logPlatformSessionSummary(constants.PlatformTwitchToken, netPoints, online, len(m.streamers), watching)
	}
	if m.kickMiner != nil {
		netPoints, online, total, watching := 0, 0, 0, 0
		for _, account := range m.kickMiner.Snapshot() {
			for _, streamer := range account.Streamers {
				total++
				if streamer.StatusKnown && streamer.Online {
					online++
				}
				if streamer.Watching && streamer.Online {
					watching++
				}
				if streamer.HasPoints && streamer.InitialPointsSet {
					netPoints += streamer.Points - streamer.InitialPoints
				}
			}
		}
		m.logPlatformSessionSummary(constants.PlatformKickToken, netPoints, online, total, watching)
	}
}

func (m *Miner) logPlatformSessionSummary(platform string, netPoints, online, total, watching int) {
	if m.anonymizeLogs() {
		m.logger.Printf("%s Net points [hidden] | online %d of %d | watching %d", platform, online, total, watching)
		return
	}
	sign, color, value := signedKickDelta(netPoints)
	m.logger.Printf("%s Net points %s%s%d%s | online %d of %d | watching %d", platform, color, sign, value, colorReset, online, total, watching)
}
