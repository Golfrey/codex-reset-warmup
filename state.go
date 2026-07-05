package main

// State, timers, and shutdown helpers.
// Think of pluginState as the one small in-memory database this plugin owns.
import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// hostClient hides the cgo boundary so tests can use a fake host.
type hostClient interface {
	Call(method string, payload any) (json.RawMessage, error)
}

type stoppableTimer interface {
	Stop() bool
}

type timerFactory func(time.Duration, func()) stoppableTimer

type realTimer struct {
	timer *time.Timer
}

func (t realTimer) Stop() bool {
	if t.timer == nil {
		return false
	}
	return t.timer.Stop()
}

// pluginState holds the current config plus transient timer/result state.
type pluginState struct {
	host         hostClient
	secret       string
	timerFactory timerFactory
	parseReset   func(pluginapi.UsageRecord, time.Time, pluginConfig) (resetBoundary, bool)

	mu      sync.Mutex
	cfg     pluginConfig
	timers  map[string]*timerEntry
	results map[string]warmupResult

	idleCheckTimer   stoppableTimer
	idleCheckNextAt  time.Time
	idleCheckRunning bool
	idleCheckLast    idleCheckResult
}

// newPluginState wires defaults and the real timer implementation.
func newPluginState(host hostClient) *pluginState {
	return &pluginState{
		host:   host,
		secret: newSecret(),
		timerFactory: func(d time.Duration, f func()) stoppableTimer {
			return realTimer{timer: time.AfterFunc(d, f)}
		},
		parseReset: parseUsageReset,
		cfg:        defaultConfig(),
		timers:     make(map[string]*timerEntry),
		results:    make(map[string]warmupResult),
	}
}

// newSecret creates a private header value so only this plugin can steer scheduler picks.
func newSecret() string {
	buf := make([]byte, 16)
	if _, errRead := rand.Read(buf); errRead == nil {
		return hex.EncodeToString(buf)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

// shutdown stops background timers before the plugin is unloaded.
func (s *pluginState) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idleCheckTimer != nil {
		s.idleCheckTimer.Stop()
		s.idleCheckTimer = nil
	}
	s.idleCheckNextAt = time.Time{}
	s.idleCheckRunning = false
	for key, entry := range s.timers {
		if entry != nil && entry.timer != nil {
			entry.timer.Stop()
		}
		delete(s.timers, key)
	}
}
