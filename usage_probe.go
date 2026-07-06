package main

// Direct Codex usage probing for idle checks.
import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	codexUsageProbeURL       = "https://chatgpt.com/backend-api/wham/usage"
	codexUsageProbeUserAgent = "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal"
)

func (s *pluginState) probeCodexUsage(auth pluginapi.HostAuthFileEntry, cfg pluginConfig, now time.Time) (resetBoundary, bool, error) {
	authIndex := strings.TrimSpace(auth.AuthIndex)
	if authIndex == "" {
		return resetBoundary{}, false, fmt.Errorf("auth_index is required")
	}
	material, errAuth := s.getCodexAuthMaterial(authIndex, cfg)
	if errAuth != nil {
		return resetBoundary{}, false, errAuth
	}
	if material.IsAPIKey {
		return resetBoundary{}, false, fmt.Errorf("codex access token not found in auth file")
	}
	resp, errProbe := s.executeCodexUsageProbe(material)
	if errProbe != nil {
		return resetBoundary{}, false, errProbe
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resetBoundary{}, false, fmt.Errorf("codex usage probe returned status %d", resp.StatusCode)
	}
	boundary, ok := parseCodexUsageProbeReset(resp.Body, now, cfg)
	if !ok {
		return resetBoundary{}, false, nil
	}
	boundary.AuthIndex = authIndex
	if boundary.AuthID == "" {
		boundary.AuthID = strings.TrimSpace(auth.ID)
	}
	return boundary, true, nil
}

func (s *pluginState) executeCodexUsageProbe(material codexAuthMaterial) (pluginapi.HTTPResponse, error) {
	if s.host == nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("host callbacks unavailable")
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+material.Token)
	headers.Set("Content-Type", "application/json")
	headers.Set("User-Agent", codexUsageProbeUserAgent)
	if material.AccountID != "" {
		headers.Set("Chatgpt-Account-Id", material.AccountID)
	}
	raw, errCall := s.host.Call(pluginabi.MethodHostHTTPDo, pluginapi.HTTPRequest{
		Method:  http.MethodGet,
		URL:     codexUsageProbeURL,
		Headers: headers,
	})
	if errCall != nil {
		return pluginapi.HTTPResponse{}, errCall
	}
	var resp pluginapi.HTTPResponse
	if errUnmarshal := json.Unmarshal(raw, &resp); errUnmarshal != nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("decode host.http.do result: %w", errUnmarshal)
	}
	return resp, nil
}

func parseCodexUsageProbeReset(body []byte, now time.Time, cfg pluginConfig) (resetBoundary, bool) {
	var object map[string]json.RawMessage
	if errUnmarshal := json.Unmarshal(body, &object); errUnmarshal != nil {
		return resetBoundary{}, false
	}
	rateLimit := rawObjectField(object, "rate_limit", "rateLimit")
	if rateLimit == nil {
		return resetBoundary{}, false
	}
	var candidates []resetBoundary
	if cfg.ScheduleFiveHour {
		if boundary, ok := parseCodexUsageProbeWindow(rawObjectField(rateLimit, "primary_window", "primaryWindow"), "5h", fiveHourSeconds, now); ok {
			candidates = append(candidates, boundary)
		}
	}
	if cfg.ScheduleWeekly {
		if boundary, ok := parseCodexUsageProbeWindow(rawObjectField(rateLimit, "secondary_window", "secondaryWindow"), "weekly", weeklySeconds, now); ok {
			candidates = append(candidates, boundary)
		}
	}
	candidates = futureBoundaries(candidates, now)
	if len(candidates) == 0 {
		return resetBoundary{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].ResetAt.Before(candidates[j].ResetAt)
	})
	return candidates[0], true
}

func parseCodexUsageProbeWindow(object map[string]json.RawMessage, window string, wantSeconds int64, now time.Time) (resetBoundary, bool) {
	if object == nil {
		return resetBoundary{}, false
	}
	seconds, okSeconds := rawIntField(object, "limit_window_seconds", "limitWindowSeconds")
	if !okSeconds || seconds != wantSeconds {
		return resetBoundary{}, false
	}
	if resetAt, okResetAt := rawIntField(object, "reset_at", "resetAt"); okResetAt && resetAt > 0 {
		return resetBoundary{Window: window, ResetAt: time.Unix(resetAt, 0)}, true
	}
	if resetAfter, okResetAfter := rawIntField(object, "reset_after_seconds", "resetAfterSeconds"); okResetAfter && resetAfter >= 0 {
		return resetBoundary{Window: window, ResetAt: now.Add(time.Duration(resetAfter) * time.Second)}, true
	}
	return resetBoundary{}, false
}

func rawObjectField(object map[string]json.RawMessage, keys ...string) map[string]json.RawMessage {
	for _, key := range keys {
		raw, ok := object[key]
		if !ok || len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var nested map[string]json.RawMessage
		if errUnmarshal := json.Unmarshal(raw, &nested); errUnmarshal == nil {
			return nested
		}
	}
	return nil
}

func rawIntField(object map[string]json.RawMessage, keys ...string) (int64, bool) {
	for _, key := range keys {
		raw, ok := object[key]
		if !ok || len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var number json.Number
		if errUnmarshal := json.Unmarshal(raw, &number); errUnmarshal == nil {
			if parsed, errParse := number.Int64(); errParse == nil {
				return parsed, true
			}
			if parsedFloat, errParse := strconv.ParseFloat(number.String(), 64); errParse == nil {
				return int64(parsedFloat), true
			}
		}
		var text string
		if errUnmarshal := json.Unmarshal(raw, &text); errUnmarshal == nil {
			parsed, errParse := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
			if errParse == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}
