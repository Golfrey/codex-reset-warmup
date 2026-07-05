package main

// Management page request handling and small HTML renderer.
// The renderer is intentionally plain: no templates, no assets, just easy-to-debug HTML.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// handleManagement optionally runs a manual warmup, then renders the current status page.
func (s *pluginState) handleManagement(raw []byte) ([]byte, error) {
	var req managementRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}
	var notice string
	var noticeError string
	if strings.EqualFold(strings.TrimSpace(req.Query.Get("action")), "warmup") {
		result := s.runManualWarmup(req.Query.Get("auth_index"))
		if result.Error != "" {
			noticeError = result.Error
		} else {
			notice = fmt.Sprintf("Manual warmup sent for auth_index %s with status %d.", result.AuthIndex, result.StatusCode)
		}
	}
	auths, errAuths := s.listCodexAuths()
	if errAuths != nil {
		noticeError = errAuths.Error()
	}
	return okEnvelope(htmlResponse(http.StatusOK, s.renderStatusPage(auths, notice, noticeError)))
}

// renderStatusPage takes a snapshot first, then writes each visible section in order.
func (s *pluginState) renderStatusPage(auths []pluginapi.HostAuthFileEntry, notice string, noticeError string) []byte {
	snapshot := s.statusPageSnapshot()

	var out bytes.Buffer
	writeStatusPageStart(&out, notice, noticeError)
	writeConfigDefinitions(&out, snapshot)
	writeManualWarmupTable(&out, auths)
	writeTimersTable(&out, snapshot.timers)
	writeResultsTable(&out, snapshot.results)
	out.WriteString("</body></html>")
	return out.Bytes()
}

func (s *pluginState) statusPageSnapshot() statusPageSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := statusPageSnapshot{
		cfg:         s.cfg,
		idleNextAt:  s.idleCheckNextAt,
		idleRunning: s.idleCheckRunning,
		idleLast:    s.idleCheckLast,
		timers:      make([]timerEntry, 0, len(s.timers)),
		results:     make([]warmupResult, 0, len(s.results)),
	}
	for _, entry := range s.timers {
		if entry != nil {
			copyEntry := *entry
			copyEntry.timer = nil
			snapshot.timers = append(snapshot.timers, copyEntry)
		}
	}
	for _, result := range s.results {
		snapshot.results = append(snapshot.results, result)
	}

	sort.Slice(snapshot.timers, func(i, j int) bool {
		return snapshot.timers[i].ResetAt.Before(snapshot.timers[j].ResetAt)
	})
	sort.Slice(snapshot.results, func(i, j int) bool {
		return snapshot.results[i].RanAt.After(snapshot.results[j].RanAt)
	})
	return snapshot
}

func writeStatusPageStart(out *bytes.Buffer, notice string, noticeError string) {
	out.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>Codex Reset Warmup</title>")
	out.WriteString("<style>body{font-family:-apple-system,BlinkMacSystemFont,\"Segoe UI\",sans-serif;margin:2rem;line-height:1.45;color:#1f2933}table{border-collapse:collapse;width:100%;margin:1rem 0}th,td{border:1px solid #d0d7de;padding:.45rem;text-align:left}th{background:#f6f8fa}code{background:#f6f8fa;border-radius:4px;padding:.1rem .3rem}.notice{color:#067647}.error{color:#b42318}</style>")
	out.WriteString("</head><body><h1>Codex Reset Warmup</h1>")
	if strings.TrimSpace(notice) != "" {
		out.WriteString("<p class=\"notice\">")
		out.WriteString(html.EscapeString(notice))
		out.WriteString("</p>")
	}
	if strings.TrimSpace(noticeError) != "" {
		out.WriteString("<p class=\"error\">")
		out.WriteString(html.EscapeString(noticeError))
		out.WriteString("</p>")
	}
}

func writeConfigDefinitions(out *bytes.Buffer, snapshot statusPageSnapshot) {
	cfg := snapshot.cfg
	out.WriteString("<dl>")
	writeDefinition(out, "Enabled", strconv.FormatBool(cfg.Enabled))
	writeDefinition(out, "Warmup model", cfg.WarmupModel)
	writeDefinition(out, "Manual mode", cfg.ManualMode)
	if cfg.ManualMode == "http" {
		writeDefinition(out, "CPA base URL", cfg.CPABaseURL)
	}
	if cfg.ManualMode == "direct_codex" {
		writeDefinition(out, "Codex base URL", cfg.CodexBaseURL)
	}
	writeDefinition(out, "Idle check", strconv.FormatBool(cfg.IdleCheckEnabled))
	writeDefinition(out, "Idle check mode", cfg.IdleCheckMode)
	writeDefinition(out, "Idle check interval minutes", strconv.Itoa(cfg.IdleCheckIntervalMinutes))
	if snapshot.idleRunning {
		writeDefinition(out, "Idle check running", "true")
	} else {
		writeDefinition(out, "Idle check running", "false")
	}
	if !snapshot.idleNextAt.IsZero() {
		writeDefinition(out, "Next idle check", snapshot.idleNextAt.Format(time.RFC3339))
	}
	if !snapshot.idleLast.RanAt.IsZero() {
		last := fmt.Sprintf("%s checked=%d skipped=%d failed=%d", snapshot.idleLast.RanAt.Format(time.RFC3339), snapshot.idleLast.Checked, snapshot.idleLast.Skipped, snapshot.idleLast.Failed)
		if snapshot.idleLast.Error != "" {
			last += " error=" + snapshot.idleLast.Error
		}
		writeDefinition(out, "Last idle check", last)
	}
	writeDefinition(out, "5-hour windows", strconv.FormatBool(cfg.ScheduleFiveHour))
	writeDefinition(out, "Weekly windows", strconv.FormatBool(cfg.ScheduleWeekly))
	out.WriteString("</dl>")
}

func writeManualWarmupTable(out *bytes.Buffer, auths []pluginapi.HostAuthFileEntry) {
	out.WriteString("<h2>Manual Warmup</h2><table><thead><tr><th>Auth index</th><th>Name</th><th>Status</th><th>Action</th></tr></thead><tbody>")
	if len(auths) == 0 {
		out.WriteString("<tr><td colspan=\"4\">No Codex auths found.</td></tr>")
	}
	for _, auth := range auths {
		authIndex := strings.TrimSpace(auth.AuthIndex)
		out.WriteString("<tr>")
		writeCodeCell(out, authIndex)
		writeTableCell(out, auth.Name, "")
		writeTableCell(out, auth.Status, "")
		out.WriteString("<td>")
		if authIndex == "" {
			out.WriteString("Missing auth index")
		} else {
			out.WriteString("<a href=\"?action=warmup&amp;auth_index=")
			out.WriteString(url.QueryEscape(authIndex))
			out.WriteString("\">Warm up now</a>")
		}
		out.WriteString("</td></tr>")
	}
	out.WriteString("</tbody></table>")
}

func writeTimersTable(out *bytes.Buffer, timers []timerEntry) {
	out.WriteString("<h2>Timers</h2><table><thead><tr><th>Auth index</th><th>Auth ID</th><th>Window</th><th>Reset at</th></tr></thead><tbody>")
	if len(timers) == 0 {
		out.WriteString("<tr><td colspan=\"4\">No timers registered.</td></tr>")
	}
	for _, entry := range timers {
		out.WriteString("<tr>")
		writeCodeCell(out, entry.AuthIndex)
		writeCodeCell(out, entry.AuthID)
		writeTableCell(out, entry.Window, "")
		writeTableCell(out, entry.ResetAt.Format(time.RFC3339), "")
		out.WriteString("</tr>")
	}
	out.WriteString("</tbody></table>")
}

func writeResultsTable(out *bytes.Buffer, results []warmupResult) {
	out.WriteString("<h2>Recent Warmups</h2><table><thead><tr><th>Auth index</th><th>Auth ID</th><th>Ran at</th><th>Status</th><th>Error</th></tr></thead><tbody>")
	if len(results) == 0 {
		out.WriteString("<tr><td colspan=\"5\">No warmups have run.</td></tr>")
	}
	for _, result := range results {
		out.WriteString("<tr>")
		writeCodeCell(out, result.AuthIndex)
		writeCodeCell(out, result.AuthID)
		writeTableCell(out, result.RanAt.Format(time.RFC3339), "")
		writeTableCell(out, strconv.Itoa(result.StatusCode), "")
		writeTableCell(out, result.Error, "error")
		out.WriteString("</tr>")
	}
	out.WriteString("</tbody></table>")
}

func writeCodeCell(out *bytes.Buffer, value string) {
	out.WriteString("<td><code>")
	out.WriteString(html.EscapeString(value))
	out.WriteString("</code></td>")
}

func writeTableCell(out *bytes.Buffer, value string, class string) {
	if class == "" {
		out.WriteString("<td>")
	} else {
		out.WriteString("<td class=\"")
		out.WriteString(html.EscapeString(class))
		out.WriteString("\">")
	}
	out.WriteString(html.EscapeString(value))
	out.WriteString("</td>")
}

func writeDefinition(out *bytes.Buffer, key string, value string) {
	out.WriteString("<dt>")
	out.WriteString(html.EscapeString(key))
	out.WriteString("</dt><dd>")
	out.WriteString(html.EscapeString(value))
	out.WriteString("</dd>")
}

func htmlResponse(statusCode int, body []byte) managementResponse {
	return managementResponse{
		StatusCode: statusCode,
		Headers: http.Header{
			"content-type": []string{resourceContentType},
		},
		Body: body,
	}
}
