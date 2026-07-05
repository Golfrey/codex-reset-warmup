package main

// Periodic idle checks for Codex auths that do not currently have reset timers.
import (
	"strings"
	"time"
)

// restartIdleCheckLocked refreshes the watchdog timer after config changes. Caller must hold s.mu.
func (s *pluginState) restartIdleCheckLocked(now time.Time) {
	if s.idleCheckTimer != nil {
		s.idleCheckTimer.Stop()
		s.idleCheckTimer = nil
	}
	s.idleCheckNextAt = time.Time{}
	if !s.cfg.Enabled || !s.cfg.IdleCheckEnabled {
		return
	}
	s.scheduleIdleCheckLocked(now, startupIdleCheckDelay)
}

func (s *pluginState) scheduleIdleCheckLocked(now time.Time, delay time.Duration) {
	if delay <= 0 {
		delay = time.Duration(defaultIdleCheckIntervalMinutes) * time.Minute
	}
	s.idleCheckNextAt = now.Add(delay)
	s.idleCheckTimer = s.timerFactory(delay, s.fireIdleCheck)
}

// fireIdleCheck prevents overlapping idle checks, then schedules the next one after work finishes.
func (s *pluginState) fireIdleCheck() {
	s.mu.Lock()
	cfg := s.cfg
	s.idleCheckTimer = nil
	s.idleCheckNextAt = time.Time{}
	if !cfg.Enabled || !cfg.IdleCheckEnabled {
		s.mu.Unlock()
		return
	}
	if s.idleCheckRunning {
		s.scheduleIdleCheckLocked(time.Now(), time.Duration(cfg.IdleCheckIntervalMinutes)*time.Minute)
		s.mu.Unlock()
		return
	}
	s.idleCheckRunning = true
	s.mu.Unlock()

	result := s.runIdleCheck(cfg)

	s.mu.Lock()
	s.idleCheckRunning = false
	s.idleCheckLast = result
	current := s.cfg
	if current.Enabled && current.IdleCheckEnabled {
		s.scheduleIdleCheckLocked(time.Now(), time.Duration(current.IdleCheckIntervalMinutes)*time.Minute)
	}
	s.mu.Unlock()
}

// runIdleCheck warms auths with no active timer so silent accounts can still reveal reset headers.
func (s *pluginState) runIdleCheck(cfg pluginConfig) idleCheckResult {
	result := idleCheckResult{RanAt: time.Now()}
	auths, errAuths := s.listCodexAuths()
	if errAuths != nil {
		result.Error = errAuths.Error()
		return result
	}
	for _, auth := range auths {
		authIndex := strings.TrimSpace(auth.AuthIndex)
		if authIndex == "" {
			continue
		}
		if s.hasTimer(authIndex) {
			result.Skipped++
			continue
		}
		entry := timerEntry{
			AuthIndex: authIndex,
			AuthID:    strings.TrimSpace(auth.ID),
			Window:    "idle_check",
		}
		warmup := s.runWarmupWithMode(entry, cfg, cfg.IdleCheckMode)
		s.mu.Lock()
		s.results[authIndex] = warmup
		s.mu.Unlock()
		result.Checked++
		if warmup.Error != "" {
			result.Failed++
		}
	}
	return result
}

func (s *pluginState) hasTimer(authIndex string) bool {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.timers[authIndex]
	return exists
}
