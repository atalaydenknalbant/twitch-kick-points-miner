package twitchchannelpointsminer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchChannelPointsMiner/constants"

	"github.com/gorilla/websocket"
)

const (
	kickBaseURL       = "https://kick.com"
	kickChannelAPI    = "https://kick.com/api/v2/channels/%s"
	kickFollowedAPI   = "https://kick.com/api/v2/channels/followed"
	kickLivestreamAPI = "https://kick.com/api/v2/channels/%s/livestream"
	kickPointsAPI     = "https://kick.com/api/v2/channels/%s/points"
	kickWSTokenAPI    = "https://websockets.kick.com/viewer/v1/token"
	kickWSConnect     = "wss://websockets.kick.com/viewer/v1/connect"
	kickClientToken   = "e1393935a959b4020a4491574f6490129f678acdaa92760471263db43487f823"
	kickPollWorkers   = 4
)

type KickSettings struct {
	Enabled                     bool                `json:"enabled"`
	SetupCompleted              bool                `json:"setup_completed"`
	SetupVersion                int                 `json:"setup_version"`
	Accounts                    []KickAccountConfig `json:"accounts"`
	CheckIntervalSeconds        int                 `json:"check_interval"`
	PointsIntervalSeconds       int                 `json:"points_interval"`
	HandshakeIntervalSeconds    int                 `json:"handshake_interval"`
	WatchEventIntervalSeconds   int                 `json:"watch_event_interval"`
	ReconnectCooldownSeconds    int                 `json:"reconnect_cooldown"`
	ConnectionStaggerMinSeconds int                 `json:"connection_stagger_min"`
	ConnectionStaggerMaxSeconds int                 `json:"connection_stagger_max"`
}

type KickAccountConfig struct {
	Alias          string   `json:"alias"`
	Token          string   `json:"-"`
	CredentialFile string   `json:"credential_file,omitempty"`
	Streamers      []string `json:"streamers"`
	MaxConcurrent  int      `json:"max_concurrent"`
}

func (a *KickAccountConfig) UnmarshalJSON(data []byte) error {
	var stored struct {
		Alias          string   `json:"alias"`
		Token          string   `json:"token"`
		CredentialFile string   `json:"credential_file"`
		Streamers      []string `json:"streamers"`
		MaxConcurrent  int      `json:"max_concurrent"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}
	a.Alias = stored.Alias
	a.Token = stored.Token
	a.CredentialFile = stored.CredentialFile
	a.Streamers = stored.Streamers
	a.MaxConcurrent = stored.MaxConcurrent
	return nil
}

func (s *KickSettings) Default() {
	if s.CheckIntervalSeconds <= 0 {
		s.CheckIntervalSeconds = 120
	}
	if s.PointsIntervalSeconds <= 0 {
		s.PointsIntervalSeconds = 150
	}
	if s.HandshakeIntervalSeconds <= 0 {
		s.HandshakeIntervalSeconds = 30
	}
	if s.WatchEventIntervalSeconds <= 0 {
		s.WatchEventIntervalSeconds = 10
	}
	if s.ReconnectCooldownSeconds <= 0 {
		s.ReconnectCooldownSeconds = 60
	}
	if s.ConnectionStaggerMinSeconds <= 0 {
		s.ConnectionStaggerMinSeconds = 3
	}
	if s.ConnectionStaggerMaxSeconds < s.ConnectionStaggerMinSeconds {
		s.ConnectionStaggerMaxSeconds = s.ConnectionStaggerMinSeconds + 5
	}
	for i := range s.Accounts {
		if strings.TrimSpace(s.Accounts[i].Alias) == "" {
			s.Accounts[i].Alias = fmt.Sprintf("Kick Account %d", i+1)
		}
		if s.Accounts[i].MaxConcurrent <= 0 {
			s.Accounts[i].MaxConcurrent = 2
		}
		s.Accounts[i].Streamers = normalizeStreamerList(s.Accounts[i].Streamers)
	}
}

type KickMiner struct {
	settings KickSettings
	logger   *Logger
	mu       sync.Mutex
	runtimes []*kickAccountRuntime
}

func NewKickMiner(settings KickSettings, logger *Logger) *KickMiner {
	settings.Default()
	return &KickMiner{
		settings: settings,
		logger:   logger,
	}
}

func (m *KickMiner) Start(stop <-chan struct{}) {
	if m == nil || !m.settings.Enabled {
		return
	}
	if len(m.settings.Accounts) == 0 {
		m.logger.Printf("%s miner enabled but no accounts configured", constants.PlatformKickToken)
		return
	}
	m.logger.Printf("%s Channel Points Miner enabled with %d account(s)", constants.PlatformKickToken, len(m.settings.Accounts))
	for _, account := range m.settings.Accounts {
		account := account
		if strings.TrimSpace(account.Token) == "" {
			m.logger.Printf("%s [%s] skipped: token missing", constants.PlatformKickToken, account.displayName())
			continue
		}
		runtime := newKickAccountRuntime(account, m.settings, m.logger)
		m.mu.Lock()
		m.runtimes = append(m.runtimes, runtime)
		m.mu.Unlock()
		go runtime.run(stop)
	}
}

func (m *KickMiner) Snapshot() []kickAccountSnapshot {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	runtimes := append([]*kickAccountRuntime(nil), m.runtimes...)
	m.mu.Unlock()

	snapshots := make([]kickAccountSnapshot, 0, len(runtimes))
	for _, runtime := range runtimes {
		if runtime == nil {
			continue
		}
		snapshots = append(snapshots, runtime.snapshot())
	}
	return snapshots
}

func (m *KickMiner) LogSummary() {
	if m == nil || m.logger == nil {
		return
	}
	snapshots := m.Snapshot()
	if len(snapshots) == 0 {
		return
	}

	hide := m.logger.settings.AnonymizeLogs
	totalPointsChange := 0
	for _, account := range snapshots {
		for _, streamer := range account.Streamers {
			if streamer.HasPoints && streamer.InitialPointsSet {
				totalPointsChange += streamer.Points - streamer.InitialPoints
			}
		}
	}

	if hide {
		m.logger.EmojiPrintf(":chart_with_upwards_trend:", "%s Total Points gained: [hidden]", constants.PlatformKickToken)
	} else {
		sign, color, value := signedKickDelta(totalPointsChange)
		m.logger.EmojiPrintf(":chart_with_upwards_trend:", "%s Total Points gained: %s%s%d%s", constants.PlatformKickToken, color, sign, value, colorReset)
	}

	for _, account := range snapshots {
		for _, streamer := range account.Streamers {
			if !streamer.HasPoints && len(streamer.History) == 0 {
				continue
			}
			total := 0
			if streamer.InitialPointsSet {
				total = streamer.Points - streamer.InitialPoints
			}
			if total == 0 && len(streamer.History) == 0 {
				continue
			}
			sign, signColor, value := signedKickDelta(total)
			name := kickDisplayName(streamer.Name)
			points := formatKickPoints(streamer.Points, streamer.HasPoints, hide)
			if hide {
				name = "[hidden]"
				m.logger.EmojiPrintf(":moneybag:", "%s [%s] %s (%s%s%s points), Total Points [hidden]", constants.PlatformKickToken, account.Alias, name, colorCyan, points, colorReset)
			} else {
				m.logger.EmojiPrintf(":moneybag:", "%s [%s] %s (%s%s%s points), Total Points %s%s%d%s", constants.PlatformKickToken, account.Alias, name, colorCyan, points, colorReset, signColor, sign, value, colorReset)
			}
			for reason, entry := range streamer.History {
				if hide {
					m.logger.Printf("                         %s (%d times, [hidden])", reason, entry.Count)
					continue
				}
				historySign, _, historyValue := signedKickDelta(entry.Amount)
				m.logger.Printf("                         %s (%d times, %s%d points)", reason, entry.Count, historySign, historyValue)
			}
		}
	}
}

type kickAccountRuntime struct {
	account  KickAccountConfig
	settings KickSettings
	logger   *Logger
	client   *kickClient

	mu        sync.Mutex
	streamers map[string]*kickStreamerState
	order     []string
	followed  bool
	loaded    bool
}

type kickStreamerState struct {
	Name             string
	Priority         int
	Online           bool
	Watching         bool
	StatusKnown      bool
	StatusLogged     bool
	ChannelID        int64
	UserID           int64
	StreamID         int64
	Points           int
	HasPoints        bool
	InitialPoints    int
	InitialPointsSet bool
	History          map[string]kickPointsHistoryEntry
	CancelFunc       context.CancelFunc
}

type kickPointsHistoryEntry struct {
	Count  int
	Amount int
}

type kickAccountSnapshot struct {
	Alias     string
	Streamers []kickStreamerSnapshot
}

type kickStreamerSnapshot struct {
	Name             string
	Online           bool
	Watching         bool
	StatusKnown      bool
	Points           int
	HasPoints        bool
	InitialPoints    int
	InitialPointsSet bool
	History          map[string]kickPointsHistoryEntry
}

func newKickAccountRuntime(account KickAccountConfig, settings KickSettings, logger *Logger) *kickAccountRuntime {
	account.Streamers = normalizeStreamerList(account.Streamers)
	followed := len(account.Streamers) == 0
	streamers := make(map[string]*kickStreamerState, len(account.Streamers))
	for idx, name := range account.Streamers {
		streamers[name] = &kickStreamerState{
			Name:     name,
			Priority: idx,
		}
	}
	return &kickAccountRuntime{
		account:   account,
		settings:  settings,
		logger:    logger,
		client:    newKickClient(account.Token),
		streamers: streamers,
		order:     append([]string(nil), account.Streamers...),
		followed:  followed,
	}
}

func (r *kickAccountRuntime) run(stop <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-stop
		cancel()
	}()

	if r.followed {
		r.logger.Printf("%s [%s] monitoring followed channels, max active %d", constants.PlatformKickToken, r.account.displayName(), r.account.MaxConcurrent)
	} else {
		r.logger.Printf("%s [%s] monitoring %d configured streamer(s), max active %d", constants.PlatformKickToken, r.account.displayName(), len(r.order), r.account.MaxConcurrent)
	}
	loadStartedAt := time.Now()
	r.refresh(ctx)
	r.logLoaded(time.Since(loadStartedAt))
	r.setLoaded()
	r.rebalance(ctx)
	ticker := time.NewTicker(time.Duration(r.settings.CheckIntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.stopAll()
			return
		case <-ticker.C:
			r.refresh(ctx)
			r.rebalance(ctx)
		}
	}
}

func (r *kickAccountRuntime) refresh(ctx context.Context) {
	var added []string
	if r.followed {
		followed, err := r.client.followedChannels(ctx)
		if err != nil {
			r.logger.Printf("%s [%s] followed channels refresh failed: %v", constants.PlatformKickToken, r.account.displayName(), err)
		} else {
			var removed []string
			added, removed = r.syncStreamers(followed)
			if r.isLoaded() && (len(added) > 0 || len(removed) > 0) {
				r.logger.Printf(
					"%s [%s] followed channels refreshed: %d total, %d added, %d removed",
					constants.PlatformKickToken,
					r.account.displayName(),
					len(followed),
					len(added),
					len(removed),
				)
			}
		}
	}

	names := r.streamerOrder()
	if !r.isLoaded() {
		r.logger.EmojiPrintf(
			":hourglass_flowing_sand:",
			"%s [%s] Loading data for %d streamer(s). Please wait...",
			constants.PlatformKickToken,
			r.account.displayName(),
			len(names),
		)
	}
	r.pollStreamers(ctx, names)
	if r.isLoaded() && len(added) > 0 {
		r.logNewStreamers(added)
	}
}

func (r *kickAccountRuntime) pollStreamers(ctx context.Context, names []string) {
	if len(names) == 0 {
		return
	}

	workerCount := kickPollWorkers
	if workerCount > len(names) {
		workerCount = len(names)
	}
	jobs := make(chan string, len(names))
	for _, name := range names {
		jobs <- name
	}
	close(jobs)

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for name := range jobs {
				if ctx.Err() != nil {
					return
				}
				info, err := r.client.channelInfo(ctx, name)
				if err != nil {
					r.logger.Debugf("%s [%s] online check %s: %v", constants.PlatformKickToken, r.account.displayName(), name, err)
				} else {
					r.updateStreamerInfo(name, info)
				}
				r.pollKickPoints(ctx, name)
			}
		}()
	}
	workers.Wait()
}

func (r *kickAccountRuntime) syncStreamers(names []string) ([]string, []string) {
	names = normalizeStreamerList(names)
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}

	type stoppedWatcher struct {
		name   string
		cancel context.CancelFunc
	}
	stopped := make([]stoppedWatcher, 0)
	added := make([]string, 0)
	removed := make([]string, 0)

	r.mu.Lock()
	for _, name := range r.order {
		if _, ok := wanted[name]; ok {
			continue
		}
		removed = append(removed, name)
		if st := r.streamers[name]; st != nil && st.Watching {
			stopped = append(stopped, stoppedWatcher{name: name, cancel: st.CancelFunc})
			st.Watching = false
			st.CancelFunc = nil
		}
		delete(r.streamers, name)
	}
	for priority, name := range names {
		st := r.streamers[name]
		if st == nil {
			st = &kickStreamerState{Name: name}
			r.streamers[name] = st
			added = append(added, name)
		}
		st.Priority = priority
	}
	r.order = append(r.order[:0], names...)
	r.mu.Unlock()

	for _, watcher := range stopped {
		if watcher.cancel != nil {
			watcher.cancel()
		}
		r.logger.Printf("%s [%s] stopped watching %s", constants.PlatformKickToken, r.account.displayName(), watcher.name)
	}
	return added, removed
}

func (r *kickAccountRuntime) streamerOrder() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func (r *kickAccountRuntime) setLoaded() {
	r.mu.Lock()
	r.loaded = true
	r.mu.Unlock()
}

func (r *kickAccountRuntime) isLoaded() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loaded
}

func (r *kickAccountRuntime) logNewStreamers(names []string) {
	snapshots := make([]kickStreamerSnapshot, 0, len(names))
	r.mu.Lock()
	for _, name := range names {
		if st := r.streamers[name]; st != nil {
			st.StatusLogged = true
			snapshots = append(snapshots, snapshotKickStreamerLocked(st))
		}
	}
	r.mu.Unlock()

	for _, streamer := range snapshots {
		r.logStatus(streamer)
	}
}

func (r *kickAccountRuntime) updateStreamerInfo(name string, info kickChannelInfo) {
	var statusSnapshot kickStreamerSnapshot
	shouldLogStatus := false

	r.mu.Lock()
	st := r.streamers[name]
	if st == nil {
		r.mu.Unlock()
		return
	}
	wasOnline := st.Online
	wasStatusKnown := st.StatusKnown
	st.Online = info.Online
	st.StatusKnown = true
	if info.ChannelID != 0 {
		st.ChannelID = info.ChannelID
	}
	if info.UserID != 0 {
		st.UserID = info.UserID
	}
	st.StreamID = info.StreamID
	if st.StatusLogged && (!wasStatusKnown || st.Online != wasOnline) {
		statusSnapshot = snapshotKickStreamerLocked(st)
		shouldLogStatus = true
	}
	r.mu.Unlock()

	if shouldLogStatus {
		r.logStatus(statusSnapshot)
	}
}

func (r *kickAccountRuntime) rebalance(ctx context.Context) {
	desired := make(map[string]struct{})
	r.mu.Lock()
	for _, name := range r.order {
		st := r.streamers[name]
		if st == nil || !st.Online {
			continue
		}
		desired[name] = struct{}{}
		if len(desired) >= r.account.MaxConcurrent {
			break
		}
	}

	toStop := make([]string, 0)
	toStart := make([]string, 0)
	for _, name := range r.order {
		st := r.streamers[name]
		if st == nil {
			continue
		}
		_, want := desired[name]
		if st.Watching && !want {
			toStop = append(toStop, name)
		}
		if !st.Watching && want {
			toStart = append(toStart, name)
		}
	}
	r.mu.Unlock()

	for _, name := range toStop {
		r.stopStreamer(name)
	}
	for _, name := range toStart {
		if ctx.Err() != nil {
			return
		}
		r.startStreamer(ctx, name)
		r.sleepWithJitter(ctx)
	}
}

func (r *kickAccountRuntime) startStreamer(parent context.Context, name string) {
	r.mu.Lock()
	st := r.streamers[name]
	if st == nil || st.Watching || !st.Online || st.StreamID == 0 {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	st.Watching = true
	st.CancelFunc = cancel
	channelID := st.ChannelID
	userID := st.UserID
	streamID := st.StreamID
	r.mu.Unlock()

	r.logger.Printf("%s [%s] started watching %s", constants.PlatformKickToken, r.account.displayName(), name)
	go r.watchStreamer(ctx, name, channelID, userID, streamID)
}

func (r *kickAccountRuntime) stopStreamer(name string) {
	r.mu.Lock()
	st := r.streamers[name]
	if st == nil {
		r.mu.Unlock()
		return
	}
	cancel := st.CancelFunc
	st.CancelFunc = nil
	st.Watching = false
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.logger.Printf("%s [%s] stopped watching %s", constants.PlatformKickToken, r.account.displayName(), name)
}

func (r *kickAccountRuntime) stopAll() {
	for _, name := range r.streamerOrder() {
		r.stopStreamer(name)
	}
}

func (r *kickAccountRuntime) watchStreamer(ctx context.Context, name string, channelID, userID, streamID int64) {
	defer func() {
		r.mu.Lock()
		if st := r.streamers[name]; st != nil {
			st.Watching = false
			st.CancelFunc = nil
		}
		r.mu.Unlock()
	}()

	for ctx.Err() == nil {
		info, err := r.client.channelInfo(ctx, name)
		if err != nil {
			r.logger.Printf("%s [%s] %s refresh failed: %v", constants.PlatformKickToken, r.account.displayName(), name, err)
			if !sleepContext(ctx, time.Duration(r.settings.ReconnectCooldownSeconds)*time.Second) {
				return
			}
			continue
		}
		if info.ChannelID == 0 {
			info.ChannelID = channelID
		}
		if info.UserID == 0 {
			info.UserID = userID
		}
		if !info.Online || info.ChannelID == 0 || info.StreamID == 0 {
			r.updateStreamerInfo(name, info)
			r.logger.Printf("%s [%s] %s is offline; watcher stopped until next online check", constants.PlatformKickToken, r.account.displayName(), name)
			return
		}
		if info.UserID == 0 {
			info.UserID = info.ChannelID
		}
		r.updateStreamerInfo(name, info)
		channelID = info.ChannelID
		userID = info.UserID
		streamID = info.StreamID

		err = r.connectAndWatch(ctx, name, info)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			r.logger.Printf("%s [%s] %s websocket ended: %v", constants.PlatformKickToken, r.account.displayName(), name, err)
		}
		if !sleepContext(ctx, time.Duration(r.settings.ReconnectCooldownSeconds)*time.Second) {
			return
		}
	}
}

func (r *kickAccountRuntime) connectAndWatch(ctx context.Context, name string, info kickChannelInfo) error {
	token, err := r.client.viewerToken(ctx, name, info.ChannelID, info.UserID)
	if err != nil {
		return err
	}

	conn, err := r.client.openViewerSocket(ctx, token)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := kickSendHandshake(conn, info.ChannelID); err != nil {
		return err
	}
	if err := kickSendPing(conn); err != nil {
		return err
	}

	readErr := make(chan error, 1)
	go func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			if err := kickHandleIncoming(conn, message); err != nil {
				readErr <- err
				return
			}
		}
	}()

	handshakeTicker := time.NewTicker(time.Duration(r.settings.HandshakeIntervalSeconds) * time.Second)
	defer handshakeTicker.Stop()
	watchTicker := time.NewTicker(time.Duration(r.settings.WatchEventIntervalSeconds) * time.Second)
	defer watchTicker.Stop()
	pointsTicker := time.NewTicker(time.Duration(r.settings.PointsIntervalSeconds) * time.Second)
	defer pointsTicker.Stop()

	r.pollKickPoints(ctx, name)

	for {
		select {
		case <-ctx.Done():
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return nil
		case err := <-readErr:
			return err
		case <-handshakeTicker.C:
			if err := kickSendHandshake(conn, info.ChannelID); err != nil {
				return err
			}
			if err := kickSendPing(conn); err != nil {
				return err
			}
		case <-watchTicker.C:
			if err := kickSendWatchEvent(conn, info.ChannelID, info.StreamID); err != nil {
				return err
			}
		case <-pointsTicker.C:
			r.pollKickPoints(ctx, name)
		}
	}
}

func (r *kickAccountRuntime) pollKickPoints(ctx context.Context, name string) {
	points, ok, err := r.client.points(ctx, name)
	if err != nil {
		r.logger.Debugf("%s [%s] points %s: %v", constants.PlatformKickToken, r.account.displayName(), name, err)
		return
	}
	if !ok {
		return
	}

	r.updateKickPoints(name, points, "")
}

func (r *kickAccountRuntime) updateKickPoints(name string, points int, reason string) {
	var snapshot kickStreamerSnapshot
	var delta int
	shouldLog := false

	r.mu.Lock()
	st := r.streamers[name]
	if st == nil {
		r.mu.Unlock()
		return
	}
	old := st.Points
	hadOld := st.HasPoints
	st.Points = points
	st.HasPoints = true
	if !st.InitialPointsSet {
		st.InitialPoints = points
		st.InitialPointsSet = true
	}
	if hadOld {
		delta = points - old
		if delta != 0 {
			if reason == "" {
				if delta > 0 {
					reason = "WATCH"
				} else {
					reason = "BALANCE_CHANGE"
				}
			}
			recordKickHistory(st, reason, delta)
			snapshot = snapshotKickStreamerLocked(st)
			shouldLog = true
		}
	}
	r.mu.Unlock()

	if shouldLog {
		r.logPointsDelta(snapshot, delta, reason)
	}
}

func (r *kickAccountRuntime) logLoaded(duration time.Duration) {
	snapshot := r.snapshotAndMarkStatusLogged()
	for _, streamer := range snapshot.Streamers {
		r.logStatus(streamer)
	}
	r.logger.EmojiPrintf(":white_check_mark:", "%s [%s] %d Streamer loaded! (%s)", constants.PlatformKickToken, snapshot.Alias, len(snapshot.Streamers), formatLoadDuration(duration))
}

func (r *kickAccountRuntime) snapshot() kickAccountSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked()
}

func (r *kickAccountRuntime) snapshotAndMarkStatusLogged() kickAccountSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range r.order {
		if st := r.streamers[name]; st != nil {
			st.StatusLogged = true
		}
	}
	return r.snapshotLocked()
}

func (r *kickAccountRuntime) snapshotLocked() kickAccountSnapshot {
	streamers := make([]kickStreamerSnapshot, 0, len(r.order))
	for _, name := range r.order {
		st := r.streamers[name]
		if st == nil {
			continue
		}
		streamers = append(streamers, snapshotKickStreamerLocked(st))
	}
	return kickAccountSnapshot{
		Alias:     r.account.displayName(),
		Streamers: streamers,
	}
}

func snapshotKickStreamerLocked(st *kickStreamerState) kickStreamerSnapshot {
	history := make(map[string]kickPointsHistoryEntry, len(st.History))
	for reason, entry := range st.History {
		history[reason] = entry
	}
	return kickStreamerSnapshot{
		Name:             st.Name,
		Online:           st.Online,
		Watching:         st.Watching,
		StatusKnown:      st.StatusKnown,
		Points:           st.Points,
		HasPoints:        st.HasPoints,
		InitialPoints:    st.InitialPoints,
		InitialPointsSet: st.InitialPointsSet,
		History:          history,
	}
}

func recordKickHistory(st *kickStreamerState, reason string, amount int) {
	if st.History == nil {
		st.History = make(map[string]kickPointsHistoryEntry)
	}
	entry := st.History[reason]
	entry.Count++
	entry.Amount += amount
	st.History[reason] = entry
}

func (r *kickAccountRuntime) logStatus(streamer kickStreamerSnapshot) {
	hide := r.logger != nil && r.logger.settings.AnonymizeLogs
	name := kickDisplayName(streamer.Name)
	if hide {
		name = "[hidden]"
	}
	if !streamer.StatusKnown {
		r.logger.EmojiPrintf(":warning:", "%s [%s] %s (%s points) status is Unavailable!", constants.PlatformKickToken, r.account.displayName(), name, formatKickPoints(streamer.Points, streamer.HasPoints, hide))
		return
	}

	status := "Offline"
	emoji := ":sleeping:"
	if streamer.Online {
		status = "Online"
		emoji = ":partying_face:"
	}
	extra := ""
	if streamer.Watching {
		extra = " | Watching"
	}
	r.logger.EmojiPrintf(emoji, "%s [%s] %s (%s points) is %s!%s", constants.PlatformKickToken, r.account.displayName(), name, formatKickPoints(streamer.Points, streamer.HasPoints, hide), status, extra)
}

func (r *kickAccountRuntime) logPointsDelta(streamer kickStreamerSnapshot, delta int, reason string) {
	if delta == 0 {
		return
	}
	hide := r.logger != nil && r.logger.settings.AnonymizeLogs
	name := kickDisplayName(streamer.Name)
	if hide {
		name = "[hidden]"
	}
	points := formatKickPoints(streamer.Points, streamer.HasPoints, hide)
	sign, valueColor, value := signedKickDelta(delta)
	r.logger.EmojiPrintf(
		":rocket:",
		"%s%s%d%s → %s [%s] %s (%s%s%s points) | Reason: %s",
		valueColor,
		sign,
		value,
		colorReset,
		constants.PlatformKickToken,
		r.account.displayName(),
		name,
		colorCyan,
		points,
		colorReset,
		reason,
	)
}

func (r *kickAccountRuntime) sleepWithJitter(ctx context.Context) {
	minSeconds := r.settings.ConnectionStaggerMinSeconds
	maxSeconds := r.settings.ConnectionStaggerMaxSeconds
	if maxSeconds < minSeconds {
		maxSeconds = minSeconds
	}
	delta := maxSeconds - minSeconds
	seconds := minSeconds
	if delta > 0 {
		seconds += rand.Intn(delta + 1)
	}
	sleepContext(ctx, time.Duration(seconds)*time.Second)
}

func (a KickAccountConfig) displayName() string {
	if strings.TrimSpace(a.Alias) != "" {
		return strings.TrimSpace(a.Alias)
	}
	return "Kick Account"
}

func kickDisplayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func formatKickPoints(points int, ok bool, hide bool) string {
	if hide {
		return "[hidden]"
	}
	if !ok {
		return "?"
	}
	return formatChannelPoints(points)
}

func signedKickDelta(delta int) (string, string, int) {
	sign := "+"
	color := colorGreen
	if delta < 0 {
		sign = "-"
		color = colorRed
		delta = -delta
	}
	return sign, color, delta
}

type kickClient struct {
	token       string
	httpClient  *http.Client
	userAgent   string
	mu          sync.Mutex
	initialized bool
}

type kickChannelInfo struct {
	ChannelID int64
	UserID    int64
	StreamID  int64
	Online    bool
}

func newKickClient(token string) *kickClient {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
	}
	k := &kickClient{
		token:      token,
		httpClient: client,
		userAgent:  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
	k.setDefaultCookies()
	return k
}

func (c *kickClient) setDefaultCookies() {
	u, _ := url.Parse(kickBaseURL)
	c.httpClient.Jar.SetCookies(u, []*http.Cookie{
		{Name: "showMatureContent", Value: "true", Path: "/", Domain: "kick.com"},
		{Name: "USER_LOCALE", Value: "en", Path: "/", Domain: "kick.com"},
	})
}

func (c *kickClient) ensureSession(ctx context.Context) {
	c.mu.Lock()
	if c.initialized {
		c.mu.Unlock()
		return
	}
	c.initialized = true
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kickBaseURL, nil)
	if err != nil {
		return
	}
	c.applyHeaders(req, kickBaseURL+"/")
	resp, err := c.httpClient.Do(req)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	c.setDefaultCookies()
}

func (c *kickClient) resetSession() {
	c.mu.Lock()
	c.initialized = false
	c.mu.Unlock()
	c.setDefaultCookies()
}

func (c *kickClient) channelInfo(ctx context.Context, username string) (kickChannelInfo, error) {
	var info kickChannelInfo
	body, status, err := c.getJSON(ctx, fmt.Sprintf(kickChannelAPI, url.PathEscape(username)), fmt.Sprintf("%s/%s/", kickBaseURL, username), nil)
	if err != nil {
		return info, err
	}
	if status != http.StatusOK {
		return info, fmt.Errorf("channel status %d", status)
	}
	info, err = parseKickChannelInfo(body)
	if err != nil {
		return info, err
	}

	if !info.Online {
		streamID, err := c.livestreamID(ctx, username)
		if err == nil && streamID != 0 {
			info.StreamID = streamID
			info.Online = true
		}
	}
	return info, nil
}

func (c *kickClient) followedChannels(ctx context.Context) ([]string, error) {
	streamers := make([]string, 0)
	seenCursors := make(map[string]struct{})
	cursor := ""

	for page := 0; page < 100; page++ {
		rawURL := kickFollowedAPI
		if cursor != "" {
			rawURL += "?cursor=" + url.QueryEscape(cursor)
		}

		body, status, err := c.getJSON(ctx, rawURL, kickBaseURL+"/", nil)
		if err != nil {
			return nil, err
		}
		if status == http.StatusForbidden {
			c.resetSession()
			body, status, err = c.getJSON(ctx, rawURL, kickBaseURL+"/", nil)
			if err != nil {
				return nil, err
			}
		}
		if status != http.StatusOK {
			return nil, kickStatusError("followed channels", status, body)
		}

		names, nextCursor, err := parseKickFollowedChannelsPage(body)
		if err != nil {
			return nil, err
		}
		streamers = append(streamers, names...)
		if nextCursor == "" {
			return normalizeStreamerList(streamers), nil
		}
		if _, exists := seenCursors[nextCursor]; exists {
			return nil, fmt.Errorf("followed channels returned repeated cursor %q", nextCursor)
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = nextCursor
	}

	return nil, errors.New("followed channels exceeded 100 pages")
}

func (c *kickClient) livestreamID(ctx context.Context, username string) (int64, error) {
	body, status, err := c.getJSON(ctx, fmt.Sprintf(kickLivestreamAPI, url.PathEscape(username)), fmt.Sprintf("%s/%s/", kickBaseURL, username), nil)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("livestream status %d", status)
	}
	return extractKickInt(unwrapKickData(body), "id"), nil
}

func (c *kickClient) viewerToken(ctx context.Context, username string, channelID, userID int64) (string, error) {
	if channelID == 0 {
		return "", errors.New("missing channel id")
	}
	if userID == 0 {
		userID = channelID
	}
	headers := map[string]string{
		"Content-Type": "application/json",
		"X-Chatroom":   fmt.Sprintf("%d", channelID),
		"X-User-Id":    fmt.Sprintf("%d", userID),
	}
	body, status, err := c.getJSON(ctx, kickWSTokenAPI, fmt.Sprintf("%s/%s/", kickBaseURL, username), headers)
	if err != nil {
		return "", err
	}
	if status == http.StatusForbidden {
		c.resetSession()
		body, status, err = c.getJSON(ctx, kickWSTokenAPI, fmt.Sprintf("%s/%s/", kickBaseURL, username), headers)
		if err != nil {
			return "", err
		}
	}
	if status != http.StatusOK {
		return "", kickStatusError("viewer token", status, body)
	}
	token := extractKickString(unwrapKickData(body), "token")
	if token == "" {
		token = extractKickString(unwrapKickData(body), "websocket_token")
	}
	if token == "" {
		token = extractKickString(body, "token")
	}
	if token == "" {
		token = extractKickString(body, "websocket_token")
	}
	if token == "" {
		return "", errors.New("viewer websocket token missing")
	}
	return token, nil
}

func (c *kickClient) points(ctx context.Context, username string) (int, bool, error) {
	body, status, err := c.getJSON(ctx, fmt.Sprintf(kickPointsAPI, url.PathEscape(username)), fmt.Sprintf("%s/%s/", kickBaseURL, username), nil)
	if err != nil {
		return 0, false, err
	}
	if status == http.StatusNotFound {
		return c.pointsFromChannel(ctx, username)
	}
	if status == http.StatusForbidden || isKickTransientStatus(status) {
		return 0, false, nil
	}
	if status != http.StatusOK {
		return 0, false, fmt.Errorf("points status %d", status)
	}
	points, ok := parseKickPoints(body)
	return points, ok, nil
}

func (c *kickClient) pointsFromChannel(ctx context.Context, username string) (int, bool, error) {
	body, status, err := c.getJSON(ctx, fmt.Sprintf(kickChannelAPI, url.PathEscape(username)), fmt.Sprintf("%s/%s/", kickBaseURL, username), nil)
	if err != nil {
		return 0, false, err
	}
	if isKickTransientStatus(status) {
		return 0, false, nil
	}
	if status != http.StatusOK {
		return 0, false, nil
	}
	data := unwrapKickData(body)
	if user, ok := data["user"].(map[string]interface{}); ok {
		if points, ok := toInt(user["points"]); ok {
			return points, true, nil
		}
	}
	return 0, false, nil
}

func (c *kickClient) getJSON(ctx context.Context, rawURL, referer string, extraHeaders map[string]string) (map[string]interface{}, int, error) {
	c.ensureSession(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	c.applyHeaders(req, referer)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return map[string]interface{}{}, resp.StatusCode, nil
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		if resp.StatusCode != http.StatusOK || looksLikeHTML(body) {
			return map[string]interface{}{
				"error": strings.TrimSpace(http.StatusText(resp.StatusCode)),
			}, resp.StatusCode, nil
		}
		return nil, resp.StatusCode, err
	}
	return payload, resp.StatusCode, nil
}

func (c *kickClient) applyHeaders(req *http.Request, referer string) {
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", kickBaseURL)
	req.Header.Set("Referer", referer)
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="120", "Google Chrome";v="120", "Not?A_Brand";v="99"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("X-Client-Token", kickClientToken)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if strings.TrimSpace(c.token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.token))
	}
}

func kickStatusError(action string, status int, body map[string]interface{}) error {
	if msg, ok := body["error"].(string); ok && strings.TrimSpace(msg) != "" {
		return fmt.Errorf("%s status %d: %s", action, status, msg)
	}
	if msg, ok := body["message"].(string); ok && strings.TrimSpace(msg) != "" {
		return fmt.Errorf("%s status %d: %s", action, status, msg)
	}
	return fmt.Errorf("%s status %d", action, status)
}

func isKickTransientStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusInternalServerError || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func looksLikeHTML(body []byte) bool {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return false
	}
	text = strings.ToLower(text)
	return strings.HasPrefix(text, "<!doctype html") || strings.HasPrefix(text, "<html") || strings.HasPrefix(text, "<")
}

func (c *kickClient) openViewerSocket(ctx context.Context, token string) (*websocket.Conn, error) {
	rawURL := kickWSConnect + "?token=" + url.QueryEscape(token)
	headers := http.Header{}
	headers.Set("User-Agent", c.userAgent)
	headers.Set("Origin", kickBaseURL)
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, rawURL, headers)
	return conn, err
}

func parseKickChannelInfo(payload map[string]interface{}) (kickChannelInfo, error) {
	data := unwrapKickData(payload)
	channelID := extractKickInt(data, "id")
	if channelID == 0 {
		return kickChannelInfo{}, errors.New("channel id missing")
	}
	userID := extractKickInt(data, "user_id")
	if userID == 0 {
		if user, ok := data["user"].(map[string]interface{}); ok {
			userID = extractKickInt(user, "id")
		}
	}
	streamID := int64(0)
	if livestream, ok := data["livestream"].(map[string]interface{}); ok {
		streamID = extractKickInt(livestream, "id")
		if streamID == 0 {
			if inner, ok := livestream["data"].(map[string]interface{}); ok {
				streamID = extractKickInt(inner, "id")
			}
		}
	}
	if streamID == 0 {
		streamID = extractKickInt(data, "livestream_id")
	}
	return kickChannelInfo{
		ChannelID: channelID,
		UserID:    userID,
		StreamID:  streamID,
		Online:    streamID != 0,
	}, nil
}

func parseKickPoints(payload map[string]interface{}) (int, bool) {
	data := unwrapKickData(payload)
	if points, ok := toInt(data["points"]); ok {
		return points, true
	}
	if points, ok := toInt(payload["points"]); ok {
		return points, true
	}
	return 0, false
}

func parseKickFollowedChannelsPage(payload map[string]interface{}) ([]string, string, error) {
	data := payload
	if wrapped, ok := payload["data"].(map[string]interface{}); ok {
		data = wrapped
	}

	rawChannels, exists := data["channels"]
	if !exists {
		return nil, "", errors.New("followed channels response missing channels")
	}
	channels, ok := rawChannels.([]interface{})
	if !ok {
		return nil, "", errors.New("followed channels response has invalid channels")
	}

	names := make([]string, 0, len(channels))
	for _, rawChannel := range channels {
		channel, ok := rawChannel.(map[string]interface{})
		if !ok {
			continue
		}
		name := firstKickString(channel, "channel_slug", "slug", "user_username", "username")
		if name == "" {
			if nested, ok := channel["channel"].(map[string]interface{}); ok {
				name = firstKickString(nested, "channel_slug", "slug", "username")
			}
		}
		if name == "" {
			if user, ok := channel["user"].(map[string]interface{}); ok {
				name = firstKickString(user, "channel_slug", "username")
			}
		}
		if name != "" {
			names = append(names, name)
		}
	}

	nextCursor := kickCursorString(data["nextCursor"])
	if nextCursor == "" {
		nextCursor = kickCursorString(data["next_cursor"])
	}
	return normalizeStreamerList(names), nextCursor, nil
}

func firstKickString(data map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := data[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func kickCursorString(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return v.String()
	case float64:
		return fmt.Sprintf("%.0f", v)
	case float32:
		return fmt.Sprintf("%.0f", v)
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	default:
		return ""
	}
}

func unwrapKickData(payload map[string]interface{}) map[string]interface{} {
	if payload == nil {
		return map[string]interface{}{}
	}
	if data, ok := payload["data"].(map[string]interface{}); ok {
		return data
	}
	return payload
}

func extractKickInt(data map[string]interface{}, key string) int64 {
	if data == nil {
		return 0
	}
	n, _ := toInt64(data[key])
	return n
}

func extractKickString(data map[string]interface{}, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return value
}

func toInt(value interface{}) (int, bool) {
	n, ok := toInt64(value)
	if !ok {
		return 0, false
	}
	if n > int64(math.MaxInt) || n < int64(math.MinInt) {
		return 0, false
	}
	return int(n), true
}

func toInt64(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case string:
		if strings.TrimSpace(v) == "" {
			return 0, false
		}
		var n int64
		_, err := fmt.Sscanf(v, "%d", &n)
		return n, err == nil
	default:
		return 0, false
	}
}

func kickSendHandshake(conn *websocket.Conn, channelID int64) error {
	return conn.WriteJSON(map[string]interface{}{
		"type": "channel_handshake",
		"data": map[string]interface{}{
			"message": map[string]interface{}{
				"channelId": channelID,
			},
		},
	})
}

func kickSendPing(conn *websocket.Conn) error {
	return conn.WriteJSON(map[string]string{"type": "ping"})
}

func kickSendPong(conn *websocket.Conn) error {
	return conn.WriteJSON(map[string]string{"type": "pong"})
}

func kickSendWatchEvent(conn *websocket.Conn, channelID, streamID int64) error {
	return conn.WriteJSON(map[string]interface{}{
		"type": "user_event",
		"data": map[string]interface{}{
			"message": map[string]interface{}{
				"name":          "tracking.user.watch.livestream",
				"channel_id":    channelID,
				"livestream_id": streamID,
			},
		},
	})
}

func kickHandleIncoming(conn *websocket.Conn, message []byte) error {
	text := strings.TrimSpace(string(message))
	if text == "" {
		return nil
	}
	if text == "ping" {
		return kickSendPong(conn)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(message, &payload); err != nil {
		return nil
	}
	msgType, _ := payload["type"].(string)
	if msgType == "ping" {
		return kickSendPong(conn)
	}
	if msgType == "error" {
		return fmt.Errorf("kick websocket error: %v", payload["data"])
	}
	return nil
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
