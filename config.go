package main

// Configuration loading and normalization.
import (
	"encoding/json"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// defaultConfig is the safe baseline when no YAML is supplied.
func defaultConfig() pluginConfig {
	return pluginConfig{
		Enabled:                  true,
		WarmupModel:              defaultWarmupModel,
		WarmupPrompt:             defaultWarmupPrompt,
		WarmupStream:             false,
		ManualMode:               defaultManualMode,
		CPABaseURL:               defaultCPABaseURL,
		CodexBaseURL:             defaultCodexBaseURL,
		IdleCheckEnabled:         true,
		IdleCheckMode:            defaultIdleCheckMode,
		IdleCheckIntervalMinutes: defaultIdleCheckIntervalMinutes,
		ScheduleFiveHour:         true,
		ScheduleWeekly:           true,
	}
}

// configure starts with defaults, overlays YAML, then trims/fills values the rest of the code relies on.
func (s *pluginState) configure(raw []byte) error {
	var req lifecycleRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return errUnmarshal
		}
	}

	cfg := defaultConfig()
	if len(req.ConfigYAML) > 0 {
		if errUnmarshal := yaml.Unmarshal(req.ConfigYAML, &cfg); errUnmarshal != nil {
			return errUnmarshal
		}
	}
	cfg.WarmupModel = strings.TrimSpace(cfg.WarmupModel)
	if cfg.WarmupModel == "" {
		cfg.WarmupModel = defaultWarmupModel
	}
	if cfg.WarmupPrompt == "" {
		cfg.WarmupPrompt = defaultWarmupPrompt
	}
	cfg.ManualMode = strings.ToLower(strings.TrimSpace(cfg.ManualMode))
	if cfg.ManualMode == "" {
		cfg.ManualMode = defaultManualMode
	}
	cfg.CPABaseURL = strings.TrimRight(strings.TrimSpace(cfg.CPABaseURL), "/")
	if cfg.CPABaseURL == "" {
		cfg.CPABaseURL = defaultCPABaseURL
	}
	cfg.CodexBaseURL = strings.TrimRight(strings.TrimSpace(cfg.CodexBaseURL), "/")
	if cfg.CodexBaseURL == "" {
		cfg.CodexBaseURL = defaultCodexBaseURL
	}
	cfg.IdleCheckMode = strings.ToLower(strings.TrimSpace(cfg.IdleCheckMode))
	if cfg.IdleCheckMode == "" {
		cfg.IdleCheckMode = defaultIdleCheckMode
	}
	if cfg.IdleCheckIntervalMinutes <= 0 {
		cfg.IdleCheckIntervalMinutes = defaultIdleCheckIntervalMinutes
	}
	s.mu.Lock()
	s.cfg = cfg
	s.restartIdleCheckLocked(time.Now())
	s.mu.Unlock()
	return nil
}
