package main

// Parsing reset hints from response headers and usage-limit error bodies.
import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// parseUsageReset normalizes every reset clue and chooses the earliest future reset.
func parseUsageReset(record pluginapi.UsageRecord, now time.Time, cfg pluginConfig) (resetBoundary, bool) {
	var candidates []resetBoundary
	candidates = append(candidates, parseHeaderResets(record.ResponseHeaders, now, cfg)...)
	if failure, ok := parseFailureReset(record.Failure.Body, now, cfg); ok {
		candidates = append(candidates, failure)
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

// parseHeaderResets understands CPA-provided primary and secondary Codex reset headers.
func parseHeaderResets(headers http.Header, now time.Time, cfg pluginConfig) []resetBoundary {
	headers = canonicalHeaders(headers)
	var out []resetBoundary
	if boundary, ok := parseHeaderWindow(headers, "X-Codex-Primary-", "5h", fiveHourMinutes, now); ok && cfg.ScheduleFiveHour {
		out = append(out, boundary)
	}
	if boundary, ok := parseHeaderWindow(headers, "X-Codex-Secondary-", "weekly", weeklyMinutes, now); ok && cfg.ScheduleWeekly {
		out = append(out, boundary)
	}
	return out
}

func parseHeaderWindow(headers http.Header, prefix string, window string, wantMinutes int64, now time.Time) (resetBoundary, bool) {
	minutes, ok := parseIntHeader(headers, prefix+"Window-Minutes")
	if !ok || minutes != wantMinutes {
		return resetBoundary{}, false
	}
	if resetAt, okResetAt := parseIntHeader(headers, prefix+"Reset-At"); okResetAt && resetAt > 0 {
		return resetBoundary{Window: window, ResetAt: time.Unix(resetAt, 0)}, true
	}
	if resetAfter, okResetAfter := parseIntHeader(headers, prefix+"Reset-After-Seconds"); okResetAfter && resetAfter >= 0 {
		return resetBoundary{Window: window, ResetAt: now.Add(time.Duration(resetAfter) * time.Second)}, true
	}
	return resetBoundary{}, false
}

// parseFailureReset handles upstream usage_limit_reached JSON when headers are absent.
func parseFailureReset(body string, now time.Time, cfg pluginConfig) (resetBoundary, bool) {
	if !cfg.ScheduleFiveHour && !cfg.ScheduleWeekly {
		return resetBoundary{}, false
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return resetBoundary{}, false
	}
	var parsed usageFailureBody
	if errUnmarshal := json.Unmarshal([]byte(body), &parsed); errUnmarshal != nil {
		return resetBoundary{}, false
	}
	info := parsed
	if parsed.Error != nil {
		info = *parsed.Error
	}
	if !strings.EqualFold(strings.TrimSpace(info.Type), "usage_limit_reached") {
		return resetBoundary{}, false
	}
	if info.ResetsAt > 0 {
		return resetBoundary{Window: "usage_limit_reached", ResetAt: time.Unix(info.ResetsAt, 0)}, true
	}
	if info.ResetsInSeconds > 0 {
		return resetBoundary{Window: "usage_limit_reached", ResetAt: now.Add(time.Duration(info.ResetsInSeconds) * time.Second)}, true
	}
	return resetBoundary{}, false
}

func futureBoundaries(in []resetBoundary, now time.Time) []resetBoundary {
	out := in[:0]
	for _, boundary := range in {
		if boundary.ResetAt.After(now) {
			out = append(out, boundary)
		}
	}
	return out
}

func canonicalHeaders(headers http.Header) http.Header {
	if len(headers) == 0 {
		return nil
	}
	out := make(http.Header, len(headers))
	for key, values := range headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(key))
		if canonical == "" {
			continue
		}
		for _, value := range values {
			out.Add(canonical, value)
		}
	}
	return out
}

func parseIntHeader(headers http.Header, key string) (int64, bool) {
	for _, value := range headers.Values(key) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		parsed, errParse := strconv.ParseInt(value, 10, 64)
		if errParse == nil {
			return parsed, true
		}
	}
	return 0, false
}
