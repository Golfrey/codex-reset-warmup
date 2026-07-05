package main

// Usage-record handling and reset timer lifecycle.
import (
	"encoding/json"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func (s *pluginState) handleUsage(raw []byte) ([]byte, error) {
	var record pluginapi.UsageRecord
	if errUnmarshal := json.Unmarshal(raw, &record); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	s.handleUsageRecord(record, time.Now())
	return okEnvelope(map[string]any{})
}

// handleUsageRecord ignores non-Codex traffic, then tries to turn reset clues into one timer.
func (s *pluginState) handleUsageRecord(record pluginapi.UsageRecord, now time.Time) bool {
	authIndex := strings.TrimSpace(record.AuthIndex)
	if !strings.EqualFold(strings.TrimSpace(record.Provider), "codex") || authIndex == "" {
		return false
	}

	s.mu.Lock()
	cfg := s.cfg
	if !cfg.Enabled {
		s.mu.Unlock()
		return false
	}
	if _, exists := s.timers[authIndex]; exists {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()

	boundary, ok := s.parseReset(record, now, cfg)
	if !ok {
		return false
	}
	boundary.AuthIndex = authIndex
	if boundary.AuthID == "" {
		boundary.AuthID = strings.TrimSpace(record.AuthID)
	}
	return s.registerTimer(boundary, now)
}

// registerTimer installs exactly one timer per auth index; later records wait until it fires.
func (s *pluginState) registerTimer(boundary resetBoundary, now time.Time) bool {
	authIndex := strings.TrimSpace(boundary.AuthIndex)
	if authIndex == "" || !boundary.ResetAt.After(now) {
		return false
	}
	delay := boundary.ResetAt.Sub(now)
	entry := &timerEntry{
		AuthIndex: authIndex,
		AuthID:    strings.TrimSpace(boundary.AuthID),
		Window:    strings.TrimSpace(boundary.Window),
		ResetAt:   boundary.ResetAt,
		CreatedAt: now,
	}
	entry.timer = s.timerFactory(delay, func() {
		s.fireTimer(authIndex)
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.timers[authIndex]; exists {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		return false
	}
	s.timers[authIndex] = entry
	return true
}

// fireTimer removes the timer first so new usage records can schedule the next window during warmup.
func (s *pluginState) fireTimer(authIndex string) {
	s.mu.Lock()
	entry := s.timers[authIndex]
	delete(s.timers, authIndex)
	cfg := s.cfg
	s.mu.Unlock()
	if entry == nil || !cfg.Enabled {
		return
	}
	result := s.runWarmup(*entry, cfg)
	s.mu.Lock()
	s.results[authIndex] = result
	s.mu.Unlock()
}
