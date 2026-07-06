package main

// Management page request handling and small HTML renderer.
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

type warmupHealth struct {
	label  string
	class  string
	detail string
}

// handleManagement renders the tab or runs authenticated tab actions.
func (s *pluginState) handleManagement(raw []byte) ([]byte, error) {
	var req managementRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}

	if strings.EqualFold(strings.TrimSpace(req.Method), http.MethodPost) && strings.HasSuffix(strings.TrimSpace(req.Path), managementWarmupPath) {
		return s.handleManualWarmupPost(req)
	}

	theme := themeFromQuery(req.Query)
	notice, noticeError := noticeFromQuery(req.Query)
	auths, errAuths := s.listCodexAuths()
	if errAuths != nil {
		noticeError = errAuths.Error()
	}
	return okEnvelope(htmlResponse(http.StatusOK, s.renderStatusPage(auths, notice, noticeError, theme)))
}

func (s *pluginState) handleManualWarmupPost(req managementRequest) ([]byte, error) {
	authIndex := manualWarmupAuthIndex(req)
	theme := themeFromRequest(req)
	if authIndex == "" {
		return okEnvelope(redirectResponse(manualWarmupRedirect("warmup_error", "", "", "auth_index is required", theme)))
	}

	result := s.runManualWarmup(authIndex)
	if result.Error != "" {
		return okEnvelope(redirectResponse(manualWarmupRedirect("warmup_error", result.AuthIndex, strconv.Itoa(result.StatusCode), result.Error, theme)))
	}
	return okEnvelope(redirectResponse(manualWarmupRedirect("warmup_ok", result.AuthIndex, strconv.Itoa(result.StatusCode), "", theme)))
}

func manualWarmupAuthIndex(req managementRequest) string {
	if value := strings.TrimSpace(req.Query.Get("auth_index")); value != "" {
		return value
	}
	values, errParse := url.ParseQuery(string(req.Body))
	if errParse != nil {
		return ""
	}
	return strings.TrimSpace(values.Get("auth_index"))
}

func themeFromRequest(req managementRequest) string {
	if theme := themeFromQuery(req.Query); theme != "" {
		return theme
	}
	values, errParse := url.ParseQuery(string(req.Body))
	if errParse != nil {
		return ""
	}
	return normalizeTheme(values.Get("theme"))
}

func themeFromQuery(query url.Values) string {
	return normalizeTheme(query.Get("theme"))
}

func normalizeTheme(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "dark":
		return "dark"
	case "light":
		return "light"
	default:
		return ""
	}
}

func manualWarmupRedirect(kind string, authIndex string, status string, message string, theme string) string {
	values := url.Values{}
	if kind != "" {
		values.Set("notice", kind)
	}
	if authIndex != "" {
		values.Set("auth_index", authIndex)
	}
	if status != "" && status != "0" {
		values.Set("status", status)
	}
	if message != "" {
		values.Set("message", message)
	}
	if theme = normalizeTheme(theme); theme != "" {
		values.Set("theme", theme)
	}
	if encoded := values.Encode(); encoded != "" {
		return resourceFullPath + "?" + encoded
	}
	return resourceFullPath
}

func noticeFromQuery(query url.Values) (string, string) {
	var notice string
	var noticeError string
	switch strings.TrimSpace(query.Get("notice")) {
	case "warmup_ok":
		notice = fmt.Sprintf("Manual warmup sent for auth_index %s with status %s.", query.Get("auth_index"), query.Get("status"))
	case "warmup_error":
		noticeError = query.Get("message")
		if noticeError == "" {
			noticeError = "Manual warmup failed."
		}
	}
	return notice, noticeError
}

// renderStatusPage takes a snapshot first, then writes each visible section in order.
func (s *pluginState) renderStatusPage(auths []pluginapi.HostAuthFileEntry, notice string, noticeError string, theme string) []byte {
	snapshot := s.statusPageSnapshot()

	var out bytes.Buffer
	writeStatusPageStart(&out, notice, noticeError, theme)
	writeOperationalSummary(&out, snapshot)
	writeManualWarmupTable(&out, auths, theme)
	writeTimersTable(&out, snapshot.timers)
	writeResultsTable(&out, snapshot.results)
	writeRuntimeSettings(&out, snapshot)
	out.WriteString("</main></body></html>")
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

func writeStatusPageStart(out *bytes.Buffer, notice string, noticeError string, theme string) {
	out.WriteString("<!doctype html><html")
	if theme = normalizeTheme(theme); theme != "" {
		out.WriteString(" data-theme=\"")
		out.WriteString(html.EscapeString(theme))
		out.WriteString("\"")
	}
	out.WriteString("><head><meta charset=\"utf-8\"><title>Codex Reset Warmup</title>")
	out.WriteString("<style>:root{color-scheme:light dark;--bg:#f7f8fb;--panel:#fff;--text:#17202a;--muted:#667085;--border:#d8dee4;--soft:#f1f4f8;--accent:#2563eb;--accent-hover:#1d4ed8;--ok:#087f5b;--warn:#b42318;--code-bg:#f6f8fa;--code-border:#e5e7eb;--notice-bg:#ecfdf3;--notice-border:#abefc6;--notice-text:#067647;--error-bg:#fef3f2;--error-border:#fecdca;--error-text:#b42318;--badge-neutral-bg:#f2f4f7;--badge-neutral-text:#475467;--badge-neutral-border:#e4e7ec;--disabled-bg:#e4e7ec;--disabled-border:#d0d5dd;--disabled-text:#98a2b3;--shadow:0 1px 2px rgba(16,24,40,.04)}:root[data-theme=\"light\"]{color-scheme:light}:root[data-theme=\"dark\"]{color-scheme:dark;--bg:#0f172a;--panel:#111827;--text:#e5e7eb;--muted:#9ca3af;--border:#2f3a4f;--soft:#1f2937;--accent:#60a5fa;--accent-hover:#93c5fd;--ok:#34d399;--warn:#f87171;--code-bg:#0b1220;--code-border:#334155;--notice-bg:#052e1c;--notice-border:#166534;--notice-text:#86efac;--error-bg:#3f1212;--error-border:#7f1d1d;--error-text:#fca5a5;--badge-neutral-bg:#1f2937;--badge-neutral-text:#d1d5db;--badge-neutral-border:#374151;--disabled-bg:#1f2937;--disabled-border:#374151;--disabled-text:#6b7280;--shadow:0 1px 2px rgba(0,0,0,.28)}@media (prefers-color-scheme:dark){:root:not([data-theme=\"light\"]){color-scheme:dark;--bg:#0f172a;--panel:#111827;--text:#e5e7eb;--muted:#9ca3af;--border:#2f3a4f;--soft:#1f2937;--accent:#60a5fa;--accent-hover:#93c5fd;--ok:#34d399;--warn:#f87171;--code-bg:#0b1220;--code-border:#334155;--notice-bg:#052e1c;--notice-border:#166534;--notice-text:#86efac;--error-bg:#3f1212;--error-border:#7f1d1d;--error-text:#fca5a5;--badge-neutral-bg:#1f2937;--badge-neutral-text:#d1d5db;--badge-neutral-border:#374151;--disabled-bg:#1f2937;--disabled-border:#374151;--disabled-text:#6b7280;--shadow:0 1px 2px rgba(0,0,0,.28)}}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px/1.45 -apple-system,BlinkMacSystemFont,\"Segoe UI\",sans-serif}.page{width:min(1180px,calc(100% - 40px));margin:0 auto;padding:28px 0 42px}.topbar{display:flex;align-items:flex-start;justify-content:space-between;gap:18px;margin-bottom:20px}.eyebrow{margin:0 0 4px;color:var(--muted);font-size:12px;font-weight:700;text-transform:uppercase;letter-spacing:.04em}h1{margin:0;font-size:28px;line-height:1.2;font-weight:750;letter-spacing:0}h2{margin:0 0 12px;font-size:17px;line-height:1.3;letter-spacing:0}.section{margin-top:18px}.grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.card{background:var(--panel);border:1px solid var(--border);border-radius:8px;padding:16px;box-shadow:var(--shadow)}.metric-label{color:var(--muted);font-size:12px;font-weight:650}.metric-value{margin-top:8px;font-size:22px;font-weight:760}.metric-detail{margin-top:6px;color:var(--muted);font-size:13px}.notice,.error{border-radius:8px;padding:10px 12px;margin:0 0 12px;border:1px solid}.notice{background:var(--notice-bg);border-color:var(--notice-border);color:var(--notice-text)}.error{background:var(--error-bg);border-color:var(--error-border);color:var(--error-text)}.panel{background:var(--panel);border:1px solid var(--border);border-radius:8px;padding:16px;box-shadow:var(--shadow)}table{border-collapse:separate;border-spacing:0;width:100%;overflow:hidden;border:1px solid var(--border);border-radius:8px;background:var(--panel)}th,td{padding:11px 12px;text-align:left;border-bottom:1px solid var(--border);vertical-align:middle}th{background:var(--soft);color:var(--muted);font-size:12px;font-weight:700}tr:last-child td{border-bottom:0}code{background:var(--code-bg);border:1px solid var(--code-border);border-radius:5px;padding:2px 5px;font-size:12px}.badge{display:inline-flex;align-items:center;border-radius:999px;padding:3px 9px;font-size:12px;font-weight:700;border:1px solid transparent}.badge.ok{background:var(--notice-bg);color:var(--notice-text);border-color:var(--notice-border)}.badge.warn{background:var(--error-bg);color:var(--error-text);border-color:var(--error-border)}.badge.neutral{background:var(--badge-neutral-bg);color:var(--badge-neutral-text);border-color:var(--badge-neutral-border)}.cell-error{color:var(--warn);font-weight:650}.button{appearance:none;border:1px solid var(--accent-hover);background:var(--accent);color:#fff;border-radius:6px;padding:7px 11px;font-weight:700;cursor:pointer}.button:hover{background:var(--accent-hover)}.button:disabled{background:var(--disabled-bg);border-color:var(--disabled-border);color:var(--disabled-text);cursor:not-allowed}.settings{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px 18px;margin:0}.settings dt{color:var(--muted);font-size:12px;font-weight:650}.settings dd{margin:3px 0 0;font-weight:650;word-break:break-word}@media (max-width:900px){.grid{grid-template-columns:repeat(2,minmax(0,1fr))}.settings{grid-template-columns:1fr}}@media (max-width:620px){.page{width:min(100% - 24px,1180px);padding-top:18px}.topbar{display:block}.grid{grid-template-columns:1fr}table{display:block;overflow-x:auto;white-space:nowrap}}</style>")
	out.WriteString("</head><body><main class=\"page\"><header class=\"topbar\"><div><p class=\"eyebrow\">Plugin</p><h1>Codex Reset Warmup</h1></div></header>")
	if strings.TrimSpace(notice) != "" {
		out.WriteString("<div class=\"notice\" role=\"status\">")
		out.WriteString(html.EscapeString(notice))
		out.WriteString("</div>")
	}
	if strings.TrimSpace(noticeError) != "" {
		out.WriteString("<div class=\"error\" role=\"alert\">")
		out.WriteString(html.EscapeString(noticeError))
		out.WriteString("</div>")
	}
}

func writeOperationalSummary(out *bytes.Buffer, snapshot statusPageSnapshot) {
	health := recentWarmupHealth(snapshot.results)
	out.WriteString("<section class=\"section\"><h2>Operational Summary</h2><div class=\"grid\">")
	writeMetricCard(out, "Enabled", boolLabel(snapshot.cfg.Enabled), boolDetail(snapshot.cfg.Enabled), boolClass(snapshot.cfg.Enabled))
	writeMetricCard(out, "Scheduled timers", strconv.Itoa(len(snapshot.timers)), "active warmup timers", "neutral")
	writeMetricCard(out, "Recent warmup health", health.label, health.detail, health.class)
	nextIdle := "Not scheduled"
	if !snapshot.idleNextAt.IsZero() {
		nextIdle = formatDisplayTime(snapshot.idleNextAt)
	}
	writeMetricCard(out, "Next idle check", nextIdle, idleCheckDetail(snapshot), "neutral")
	out.WriteString("</div></section>")
}

func writeMetricCard(out *bytes.Buffer, label string, value string, detail string, class string) {
	out.WriteString("<div class=\"card\"><div class=\"metric-label\">")
	out.WriteString(html.EscapeString(label))
	out.WriteString("</div><div class=\"metric-value\"><span class=\"badge ")
	out.WriteString(html.EscapeString(class))
	out.WriteString("\">")
	out.WriteString(html.EscapeString(value))
	out.WriteString("</span></div><div class=\"metric-detail\">")
	out.WriteString(html.EscapeString(detail))
	out.WriteString("</div></div>")
}

func boolLabel(value bool) string {
	if value {
		return "Enabled"
	}
	return "Disabled"
}

func boolDetail(value bool) string {
	if value {
		return "warmup scheduling is active"
	}
	return "warmup scheduling is inactive"
}

func boolClass(value bool) string {
	if value {
		return "ok"
	}
	return "warn"
}

func idleCheckDetail(snapshot statusPageSnapshot) string {
	if snapshot.idleRunning {
		return "idle check is running"
	}
	if snapshot.cfg.IdleCheckEnabled {
		return "idle check is enabled"
	}
	return "idle check is disabled"
}

func recentWarmupHealth(results []warmupResult) warmupHealth {
	if len(results) == 0 {
		return warmupHealth{label: "No data", class: "neutral", detail: "no warmup has run"}
	}
	latest := results[0]
	detail := "latest ran at " + formatDisplayTime(latest.RanAt)
	if latest.Error != "" || latest.StatusCode < http.StatusOK || latest.StatusCode >= http.StatusMultipleChoices {
		return warmupHealth{label: "Attention", class: "warn", detail: detail}
	}
	return warmupHealth{label: "Healthy", class: "ok", detail: detail}
}

func writeRuntimeSettings(out *bytes.Buffer, snapshot statusPageSnapshot) {
	cfg := snapshot.cfg
	out.WriteString("<section class=\"section\"><div class=\"panel\"><h2>Runtime Settings</h2><dl class=\"settings\">")
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
		writeDefinition(out, "Next idle check", formatDisplayTime(snapshot.idleNextAt))
	}
	if !snapshot.idleLast.RanAt.IsZero() {
		last := fmt.Sprintf("%s checked=%d skipped=%d failed=%d", formatDisplayTime(snapshot.idleLast.RanAt), snapshot.idleLast.Checked, snapshot.idleLast.Skipped, snapshot.idleLast.Failed)
		if snapshot.idleLast.ProbeScheduled > 0 {
			last += fmt.Sprintf(" probe_scheduled=%d", snapshot.idleLast.ProbeScheduled)
		}
		if snapshot.idleLast.ProbeNoBoundary > 0 {
			last += fmt.Sprintf(" probe_no_boundary=%d", snapshot.idleLast.ProbeNoBoundary)
		}
		if snapshot.idleLast.ProbeFailed > 0 {
			last += fmt.Sprintf(" probe_failed=%d", snapshot.idleLast.ProbeFailed)
		}
		if snapshot.idleLast.Warmed > 0 {
			last += fmt.Sprintf(" warmed=%d", snapshot.idleLast.Warmed)
		}
		if snapshot.idleLast.Error != "" {
			last += " error=" + snapshot.idleLast.Error
		}
		writeDefinition(out, "Last idle check", last)
	}
	writeDefinition(out, "5-hour windows", strconv.FormatBool(cfg.ScheduleFiveHour))
	writeDefinition(out, "Weekly windows", strconv.FormatBool(cfg.ScheduleWeekly))
	out.WriteString("</dl></div></section>")
}

func writeManualWarmupTable(out *bytes.Buffer, auths []pluginapi.HostAuthFileEntry, theme string) {
	out.WriteString("<section class=\"section\"><h2>Manual Warmup</h2><table><thead><tr><th>Auth index</th><th>Name</th><th>Status</th><th>Action</th></tr></thead><tbody>")
	if len(auths) == 0 {
		out.WriteString("<tr><td colspan=\"4\">No Codex auths found.</td></tr>")
	}
	for _, auth := range auths {
		authIndex := strings.TrimSpace(auth.AuthIndex)
		out.WriteString("<tr>")
		writeCodeCell(out, authIndex)
		writeCodeCell(out, auth.Name)
		writeTableCell(out, auth.Status, "")
		out.WriteString("<td>")
		if authIndex == "" {
			out.WriteString("Missing auth index")
		} else {
			out.WriteString("<form method=\"post\" action=\"")
			out.WriteString(html.EscapeString(warmupActionPath))
			out.WriteString("\"><input type=\"hidden\" name=\"auth_index\" value=\"")
			out.WriteString(html.EscapeString(authIndex))
			out.WriteString("\">")
			if theme = normalizeTheme(theme); theme != "" {
				out.WriteString("<input type=\"hidden\" name=\"theme\" value=\"")
				out.WriteString(html.EscapeString(theme))
				out.WriteString("\">")
			}
			out.WriteString("<button class=\"button\" type=\"submit\">Run warmup</button></form>")
		}
		out.WriteString("</td></tr>")
	}
	out.WriteString("</tbody></table></section>")
}

func writeTimersTable(out *bytes.Buffer, timers []timerEntry) {
	out.WriteString("<section class=\"section\"><h2>Timers</h2><table><thead><tr><th>Auth index</th><th>Auth ID</th><th>Window</th><th>Reset at</th></tr></thead><tbody>")
	if len(timers) == 0 {
		out.WriteString("<tr><td colspan=\"4\">No timers registered.</td></tr>")
	}
	for _, entry := range timers {
		out.WriteString("<tr>")
		writeCodeCell(out, entry.AuthIndex)
		writeCodeCell(out, entry.AuthID)
		writeTableCell(out, entry.Window, "")
		writeTableCell(out, formatDisplayTime(entry.ResetAt), "")
		out.WriteString("</tr>")
	}
	out.WriteString("</tbody></table></section>")
}

func writeResultsTable(out *bytes.Buffer, results []warmupResult) {
	out.WriteString("<section class=\"section\"><h2>Recent Warmups</h2><table><thead><tr><th>Auth index</th><th>Auth ID</th><th>Ran at</th><th>Status</th><th>Error</th></tr></thead><tbody>")
	if len(results) == 0 {
		out.WriteString("<tr><td colspan=\"5\">No warmups have run.</td></tr>")
	}
	for _, result := range results {
		out.WriteString("<tr>")
		writeCodeCell(out, result.AuthIndex)
		writeCodeCell(out, result.AuthID)
		writeTableCell(out, formatDisplayTime(result.RanAt), "")
		writeTableCell(out, strconv.Itoa(result.StatusCode), "")
		writeTableCell(out, result.Error, "cell-error")
		out.WriteString("</tr>")
	}
	out.WriteString("</tbody></table></section>")
}

func writeCodeCell(out *bytes.Buffer, value string) {
	out.WriteString("<td><code>")
	out.WriteString(html.EscapeString(value))
	out.WriteString("</code></td>")
}

func formatDisplayTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("Jan 2, 2006 3:04:05 PM MST")
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

func redirectResponse(location string) managementResponse {
	return managementResponse{
		StatusCode: http.StatusSeeOther,
		Headers: http.Header{
			"Location": []string{location},
		},
	}
}
