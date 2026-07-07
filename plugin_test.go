package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type fakeTimer struct {
	stopped bool
	fire    func()
}

func (t *fakeTimer) Stop() bool {
	t.stopped = true
	return true
}

type fakeHost struct {
	calls         []hostCall
	runtimeAuth   pluginapi.HostAuthFileEntry
	httpResponse  pluginapi.HTTPResponse
	usageResponse pluginapi.HTTPResponse
	httpError     error
}

type hostCall struct {
	method  string
	payload any
}

func (h *fakeHost) Call(method string, payload any) (json.RawMessage, error) {
	h.calls = append(h.calls, hostCall{method: method, payload: payload})
	switch method {
	case "host.auth.list":
		return json.Marshal(authListResponse{
			Files: []pluginapi.HostAuthFileEntry{
				{ID: "auth-runtime", AuthIndex: "idx-1", Provider: "codex", Name: "codex-a.json"},
				{ID: "claude-runtime", AuthIndex: "idx-2", Provider: "claude", Name: "claude-a.json"},
			},
		})
	case "host.auth.get_runtime":
		if h.runtimeAuth.ID != "" || h.runtimeAuth.AuthIndex != "" {
			return json.Marshal(pluginapi.HostAuthGetRuntimeResponse{Auth: h.runtimeAuth})
		}
		return json.Marshal(pluginapi.HostAuthGetRuntimeResponse{
			Auth: pluginapi.HostAuthFileEntry{ID: "auth-runtime", AuthIndex: "idx-1", Provider: "codex"},
		})
	case "host.auth.get":
		return json.Marshal(pluginapi.HostAuthGetResponse{
			AuthIndex: "idx-1",
			Name:      "codex-a.json",
			JSON:      json.RawMessage(`{"type":"codex","access_token":"codex-token","base_url":"https://codex.example.test/backend-api/codex","account_id":"acct-1"}`),
		})
	case "host.model.execute":
		if h.httpResponse.StatusCode > 0 {
			return json.Marshal(pluginapi.HostModelExecutionResponse{
				StatusCode: h.httpResponse.StatusCode,
				Headers:    h.httpResponse.Headers,
				Body:       h.httpResponse.Body,
			})
		}
		return json.Marshal(pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK})
	case "host.http.do":
		if h.httpError != nil {
			return nil, h.httpError
		}
		if req, ok := payload.(pluginapi.HTTPRequest); ok && strings.Contains(req.URL, "wham/usage") && h.usageResponse.StatusCode > 0 {
			return json.Marshal(h.usageResponse)
		}
		if h.httpResponse.StatusCode > 0 {
			return json.Marshal(h.httpResponse)
		}
		if req, ok := payload.(pluginapi.HTTPRequest); ok && strings.Contains(req.URL, "codex.example.test") {
			return json.Marshal(pluginapi.HTTPResponse{StatusCode: http.StatusOK})
		}
		return json.Marshal(pluginapi.HTTPResponse{StatusCode: http.StatusTooManyRequests})
	default:
		return json.Marshal(map[string]any{})
	}
}

func TestParseUsageResetFiveHourHeader(t *testing.T) {
	now := time.Unix(1000, 0)
	record := pluginapi.UsageRecord{
		Provider:  "codex",
		AuthIndex: "idx-1",
		ResponseHeaders: http.Header{
			"X-Codex-Primary-Window-Minutes": []string{"300"},
			"X-Codex-Primary-Reset-At":       []string{"2000"},
		},
	}
	got, ok := parseUsageReset(record, now, defaultConfig())
	if !ok {
		t.Fatal("parseUsageReset() ok = false")
	}
	if got.Window != "5h" || !got.ResetAt.Equal(time.Unix(2000, 0)) {
		t.Fatalf("boundary = %#v, want 5h at 2000", got)
	}
}

func TestParseUsageResetWeeklyHeaderResetAfter(t *testing.T) {
	now := time.Unix(1000, 0)
	record := pluginapi.UsageRecord{
		Provider:  "codex",
		AuthIndex: "idx-1",
		ResponseHeaders: http.Header{
			"X-Codex-Secondary-Window-Minutes":      []string{"10080"},
			"X-Codex-Secondary-Reset-After-Seconds": []string{"60"},
		},
	}
	got, ok := parseUsageReset(record, now, defaultConfig())
	if !ok {
		t.Fatal("parseUsageReset() ok = false")
	}
	if got.Window != "weekly" || !got.ResetAt.Equal(now.Add(60*time.Second)) {
		t.Fatalf("boundary = %#v, want weekly after 60 seconds", got)
	}
}

func TestParseUsageResetFailureBody(t *testing.T) {
	now := time.Unix(1000, 0)
	record := pluginapi.UsageRecord{
		Provider:  "codex",
		AuthIndex: "idx-1",
		Failed:    true,
		Failure: pluginapi.UsageFailure{
			StatusCode: http.StatusTooManyRequests,
			Body:       `{"error":{"type":"usage_limit_reached","resets_in_seconds":123}}`,
		},
	}
	got, ok := parseUsageReset(record, now, defaultConfig())
	if !ok {
		t.Fatal("parseUsageReset() ok = false")
	}
	if got.Window != "usage_limit_reached" || !got.ResetAt.Equal(now.Add(123*time.Second)) {
		t.Fatalf("boundary = %#v, want usage_limit_reached after 123 seconds", got)
	}
}

func TestHandleUsageSkipsParsingWhenTimerExists(t *testing.T) {
	state := newPluginState(nil)
	state.timerFactory = func(d time.Duration, f func()) stoppableTimer {
		return &fakeTimer{fire: f}
	}
	state.mu.Lock()
	state.timers["idx-1"] = &timerEntry{AuthIndex: "idx-1", ResetAt: time.Now().Add(time.Hour)}
	state.mu.Unlock()
	called := false
	state.parseReset = func(pluginapi.UsageRecord, time.Time, pluginConfig) (resetBoundary, bool) {
		called = true
		return resetBoundary{}, false
	}
	record := pluginapi.UsageRecord{Provider: "codex", AuthIndex: "idx-1"}
	if state.handleUsageRecord(record, time.Now()) {
		t.Fatal("handleUsageRecord() registered a timer despite existing timer")
	}
	if called {
		t.Fatal("parseReset was called despite existing timer")
	}
}

func TestHandleUsageFirstUsableBoundaryWins(t *testing.T) {
	state := newPluginState(nil)
	var timer *fakeTimer
	state.timerFactory = func(d time.Duration, f func()) stoppableTimer {
		timer = &fakeTimer{fire: f}
		return timer
	}
	now := time.Unix(1000, 0)
	record := pluginapi.UsageRecord{
		Provider:  "codex",
		AuthIndex: "idx-1",
		AuthID:    "auth-1",
		ResponseHeaders: http.Header{
			"X-Codex-Primary-Window-Minutes":        []string{"300"},
			"X-Codex-Primary-Reset-After-Seconds":   []string{"120"},
			"X-Codex-Secondary-Window-Minutes":      []string{"10080"},
			"X-Codex-Secondary-Reset-After-Seconds": []string{"3600"},
		},
	}
	if !state.handleUsageRecord(record, now) {
		t.Fatal("handleUsageRecord() did not register timer")
	}
	if timer == nil {
		t.Fatal("timer was not created")
	}
	state.mu.Lock()
	entry := state.timers["idx-1"]
	state.mu.Unlock()
	if entry == nil || entry.Window != "5h" || !entry.ResetAt.Equal(now.Add(120*time.Second)) {
		t.Fatalf("timer entry = %#v, want 5h first boundary", entry)
	}
}

func TestTimerClearsBeforeWarmup(t *testing.T) {
	host := &fakeHost{}
	state := newPluginState(host)
	var timer *fakeTimer
	state.timerFactory = func(d time.Duration, f func()) stoppableTimer {
		timer = &fakeTimer{fire: f}
		return timer
	}
	now := time.Unix(1000, 0)
	record := pluginapi.UsageRecord{
		Provider:  "codex",
		AuthIndex: "idx-1",
		AuthID:    "auth-1",
		ResponseHeaders: http.Header{
			"X-Codex-Primary-Window-Minutes":      []string{"300"},
			"X-Codex-Primary-Reset-After-Seconds": []string{"1"},
		},
	}
	if !state.handleUsageRecord(record, now) {
		t.Fatal("handleUsageRecord() did not register timer")
	}
	timer.fire()
	state.mu.Lock()
	_, exists := state.timers["idx-1"]
	result := state.results["idx-1"]
	state.mu.Unlock()
	if exists {
		t.Fatal("timer entry still exists after fire")
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("warmup result = %#v, want status 200", result)
	}
}

func TestRunWarmupRefreshesRuntimeAuthID(t *testing.T) {
	host := &fakeHost{
		runtimeAuth: pluginapi.HostAuthFileEntry{ID: "auth-current", AuthIndex: "idx-1", Provider: "codex"},
	}
	state := newPluginState(host)
	result := state.runWarmup(timerEntry{
		AuthIndex: "idx-1",
		AuthID:    "auth-stale",
		Window:    "5h",
	}, defaultConfig())
	if result.StatusCode != http.StatusOK || result.AuthID != "auth-current" {
		t.Fatalf("result = %#v, want current runtime auth and status 200", result)
	}
	var executeReq hostModelExecutionRequest
	for _, call := range host.calls {
		if call.method == "host.model.execute" {
			executeReq = call.payload.(hostModelExecutionRequest)
		}
	}
	if got := executeReq.Headers.Get(headerTargetAuthID); got != "auth-current" {
		t.Fatalf("target auth header = %q, want auth-current", got)
	}
}

func TestPastResetTimesIgnored(t *testing.T) {
	state := newPluginState(nil)
	now := time.Unix(1000, 0)
	record := pluginapi.UsageRecord{
		Provider:  "codex",
		AuthIndex: "idx-1",
		ResponseHeaders: http.Header{
			"X-Codex-Primary-Window-Minutes": []string{"300"},
			"X-Codex-Primary-Reset-At":       []string{"999"},
		},
	}
	if state.handleUsageRecord(record, now) {
		t.Fatal("handleUsageRecord() registered timer for past reset")
	}
}

func TestSchedulerSelectsWarmupTarget(t *testing.T) {
	state := newPluginState(nil)
	req := pluginapi.SchedulerPickRequest{
		Options: pluginapi.SchedulerOptions{
			Headers: map[string][]string{
				headerSecret:       []string{state.secret},
				headerTargetAuthID: []string{"auth-1"},
			},
		},
		Candidates: []pluginapi.SchedulerAuthCandidate{{ID: "auth-1"}, {ID: "auth-2"}},
	}
	resp, errPick := state.pickAuthRequest(req)
	if errPick != nil {
		t.Fatalf("pickAuthRequest() error = %v", errPick)
	}
	if !resp.Handled || resp.AuthID != "auth-1" {
		t.Fatalf("pickAuthRequest() = %#v, want auth-1 handled", resp)
	}
}

func TestSchedulerMissingOrInvalidSecretUnhandled(t *testing.T) {
	state := newPluginState(nil)
	req := pluginapi.SchedulerPickRequest{
		Options: pluginapi.SchedulerOptions{
			Headers: map[string][]string{
				headerSecret:       []string{"wrong"},
				headerTargetAuthID: []string{"auth-1"},
			},
		},
		Candidates: []pluginapi.SchedulerAuthCandidate{{ID: "auth-1"}},
	}
	resp, errPick := state.pickAuthRequest(req)
	if errPick != nil {
		t.Fatalf("pickAuthRequest() error = %v", errPick)
	}
	if resp.Handled {
		t.Fatalf("pickAuthRequest() = %#v, want unhandled", resp)
	}
}

func TestSchedulerMissingTargetCandidateFailsClosed(t *testing.T) {
	state := newPluginState(nil)
	req := pluginapi.SchedulerPickRequest{
		Options: pluginapi.SchedulerOptions{
			Headers: map[string][]string{
				headerSecret:       []string{state.secret},
				headerTargetAuthID: []string{"auth-missing"},
			},
		},
		Candidates: []pluginapi.SchedulerAuthCandidate{{ID: "auth-1"}},
	}
	resp, errPick := state.pickAuthRequest(req)
	if errPick == nil || !strings.Contains(errPick.Error(), "not selectable") {
		t.Fatalf("pickAuthRequest() error = %v, want not selectable", errPick)
	}
	if resp.Handled {
		t.Fatalf("pickAuthRequest() = %#v, want no handled fallback", resp)
	}
}

func TestSchedulerMissingTargetCandidateReturnsErrorEnvelope(t *testing.T) {
	state := newPluginState(nil)
	req := pluginapi.SchedulerPickRequest{
		Options: pluginapi.SchedulerOptions{
			Headers: map[string][]string{
				headerSecret:       []string{state.secret},
				headerTargetAuthID: []string{"auth-missing"},
			},
		},
		Candidates: []pluginapi.SchedulerAuthCandidate{{ID: "auth-1"}},
	}
	rawReq, errMarshal := json.Marshal(req)
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	rawResp, errPick := state.pickAuth(rawReq)
	if errPick != nil {
		t.Fatalf("pickAuth() error = %v", errPick)
	}
	var env envelope
	if errUnmarshal := json.Unmarshal(rawResp, &env); errUnmarshal != nil {
		t.Fatalf("decode envelope: %v", errUnmarshal)
	}
	if env.OK || env.Error == nil || env.Error.Code != "warmup_auth_unavailable" || env.Error.HTTPStatus != http.StatusConflict {
		t.Fatalf("envelope = %#v, want warmup_auth_unavailable", env)
	}
}

func TestListCodexAuthsFiltersHostAuthList(t *testing.T) {
	host := &fakeHost{}
	state := newPluginState(host)
	auths, errList := state.listCodexAuths()
	if errList != nil {
		t.Fatalf("listCodexAuths() error = %v", errList)
	}
	if len(auths) != 1 || auths[0].AuthIndex != "idx-1" {
		t.Fatalf("auths = %#v, want only codex idx-1", auths)
	}
}

func TestRunManualWarmupUsesRuntimeAuthAndStoresResult(t *testing.T) {
	host := &fakeHost{}
	state := newPluginState(host)
	result := state.runManualWarmup("idx-1")
	if result.Error != "" {
		t.Fatalf("runManualWarmup() error = %s", result.Error)
	}
	if result.AuthID != "auth-runtime" || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %#v, want runtime auth and 200", result)
	}
	state.mu.Lock()
	stored := state.results["idx-1"]
	state.mu.Unlock()
	if stored.StatusCode != http.StatusOK || stored.AuthID != "auth-runtime" {
		t.Fatalf("stored result = %#v, want runtime auth and 200", stored)
	}
	var sawRuntime bool
	var sawExecute bool
	for _, call := range host.calls {
		if call.method == "host.auth.get_runtime" {
			sawRuntime = true
		}
		if call.method == "host.model.execute" {
			sawExecute = true
		}
	}
	if !sawRuntime || !sawExecute {
		t.Fatalf("host calls = %#v, want get_runtime and model.execute", host.calls)
	}
}

func TestRunManualWarmupHTTPUsesCPAEndpointAndPrivateHeaders(t *testing.T) {
	host := &fakeHost{}
	state := newPluginState(host)
	state.mu.Lock()
	state.cfg.ManualMode = "http"
	state.cfg.CPABaseURL = "http://127.0.0.1:8318"
	state.cfg.CPAAPIKey = "test-api-key"
	state.mu.Unlock()

	result := state.runManualWarmup("idx-1")
	if result.Error != "" {
		t.Fatalf("runManualWarmup() error = %s", result.Error)
	}
	if result.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("result = %#v, want HTTP warmup status 429", result)
	}
	var httpReq pluginapi.HTTPRequest
	for _, call := range host.calls {
		if call.method != "host.http.do" {
			continue
		}
		req, ok := call.payload.(pluginapi.HTTPRequest)
		if !ok {
			t.Fatalf("host.http.do payload = %T, want pluginapi.HTTPRequest", call.payload)
		}
		httpReq = req
	}
	if httpReq.URL != "http://127.0.0.1:8318/v1/chat/completions" {
		t.Fatalf("HTTP URL = %q", httpReq.URL)
	}
	if httpReq.Headers.Get("Authorization") != "Bearer test-api-key" {
		t.Fatalf("Authorization header = %q", httpReq.Headers.Get("Authorization"))
	}
	if httpReq.Headers.Get(headerSecret) != state.secret || httpReq.Headers.Get(headerTargetAuthID) != "auth-runtime" {
		t.Fatalf("private warmup headers = %#v", httpReq.Headers)
	}
}

func TestRunManualWarmupDirectCodexUsesPhysicalAuthAndBypassesCPA(t *testing.T) {
	host := &fakeHost{}
	state := newPluginState(host)
	state.mu.Lock()
	state.cfg.ManualMode = "direct_codex"
	state.cfg.WarmupModel = "gpt-5.4-mini"
	state.cfg.WarmupPrompt = "ping"
	state.mu.Unlock()

	result := state.runManualWarmup("idx-1")
	if result.Error != "" {
		t.Fatalf("runManualWarmup() error = %s", result.Error)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("result = %#v, want direct codex status 200", result)
	}
	var sawAuthGet bool
	var httpReq pluginapi.HTTPRequest
	for _, call := range host.calls {
		if call.method == "host.auth.get" {
			sawAuthGet = true
		}
		if call.method != "host.http.do" {
			continue
		}
		req, ok := call.payload.(pluginapi.HTTPRequest)
		if !ok {
			t.Fatalf("host.http.do payload = %T, want pluginapi.HTTPRequest", call.payload)
		}
		httpReq = req
	}
	if !sawAuthGet {
		t.Fatalf("host calls = %#v, want host.auth.get", host.calls)
	}
	if httpReq.URL != "https://codex.example.test/backend-api/codex/responses" {
		t.Fatalf("HTTP URL = %q", httpReq.URL)
	}
	if httpReq.Headers.Get("Authorization") != "Bearer codex-token" {
		t.Fatalf("Authorization header = %q", httpReq.Headers.Get("Authorization"))
	}
	if httpReq.Headers.Get("Accept") != "text/event-stream" {
		t.Fatalf("Accept header = %q", httpReq.Headers.Get("Accept"))
	}
	if httpReq.Headers.Get("Originator") != "codex-tui" {
		t.Fatalf("Originator header = %q", httpReq.Headers.Get("Originator"))
	}
	if httpReq.Headers.Get(headerSecret) != "" || httpReq.Headers.Get(headerTargetAuthID) != "" {
		t.Fatalf("direct request should not use CPA scheduler headers: %#v", httpReq.Headers)
	}
	var body map[string]any
	if errUnmarshal := json.Unmarshal(httpReq.Body, &body); errUnmarshal != nil {
		t.Fatalf("decode direct body: %v", errUnmarshal)
	}
	if body["model"] != "gpt-5.4-mini" || body["instructions"] != "" {
		t.Fatalf("direct body = %#v", body)
	}
	if body["stream"] != true {
		t.Fatalf("direct body stream = %#v, want true", body["stream"])
	}
	if body["store"] != false || body["parallel_tool_calls"] != true {
		t.Fatalf("direct body translator defaults missing: %#v", body)
	}
	include, ok := body["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("direct body include = %#v", body["include"])
	}
}

func TestRunManualWarmupDirectCodexSchedulesFromUsageLimitResponse(t *testing.T) {
	host := &fakeHost{
		httpResponse: pluginapi.HTTPResponse{
			StatusCode: http.StatusTooManyRequests,
			Body:       []byte(`{"error":{"type":"usage_limit_reached","message":"limit","resets_in_seconds":120}}`),
		},
	}
	state := newPluginState(host)
	var timer *fakeTimer
	state.timerFactory = func(d time.Duration, f func()) stoppableTimer {
		timer = &fakeTimer{fire: f}
		return timer
	}
	state.mu.Lock()
	state.cfg.ManualMode = "direct_codex"
	state.cfg.WarmupModel = "gpt-5.4-mini"
	state.mu.Unlock()

	result := state.runManualWarmup("idx-1")
	if result.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("result = %#v, want 429", result)
	}
	if timer == nil {
		t.Fatal("timer was not created")
	}
	state.mu.Lock()
	entry := state.timers["idx-1"]
	state.mu.Unlock()
	if entry == nil || entry.Window != "usage_limit_reached" || entry.AuthID != "auth-runtime" {
		t.Fatalf("timer entry = %#v, want usage_limit_reached for auth-runtime", entry)
	}
}

func TestParseCodexUsageProbeResetUsesEarliestEligibleWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	body := []byte(`{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_after_seconds":120},"secondary_window":{"used_percent":10,"limit_window_seconds":604800,"reset_at":1600}}}`)
	got, ok := parseCodexUsageProbeReset(body, now, defaultConfig())
	if !ok {
		t.Fatal("parseCodexUsageProbeReset() ok = false")
	}
	if got.Window != "5h" || !got.ResetAt.Equal(now.Add(120*time.Second)) {
		t.Fatalf("boundary = %#v, want 5h after 120 seconds", got)
	}
}

func TestParseCodexUsageProbeResetIgnoresDisabledWindows(t *testing.T) {
	now := time.Unix(1000, 0)
	cfg := defaultConfig()
	cfg.ScheduleFiveHour = false
	cfg.ScheduleWeekly = false
	body := []byte(`{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"reset_at":2000},"secondary_window":{"limit_window_seconds":604800,"reset_at":3000}}}`)
	if got, ok := parseCodexUsageProbeReset(body, now, cfg); ok {
		t.Fatalf("parseCodexUsageProbeReset() = %#v, want no eligible boundary", got)
	}
}

func TestIdleCheckSkipsAuthWithExistingTimer(t *testing.T) {
	host := &fakeHost{}
	state := newPluginState(host)
	state.mu.Lock()
	state.cfg.IdleCheckMode = "direct_codex"
	state.timers["idx-1"] = &timerEntry{AuthIndex: "idx-1", ResetAt: time.Now().Add(time.Hour)}
	cfg := state.cfg
	state.mu.Unlock()

	result := state.runIdleCheck(cfg)
	if result.Checked != 0 || result.Skipped != 1 || result.Failed != 0 {
		t.Fatalf("idle check result = %#v, want one skipped auth only", result)
	}
	for _, call := range host.calls {
		if call.method == "host.auth.get" || call.method == "host.http.do" {
			t.Fatalf("idle check called %s despite existing timer; calls = %#v", call.method, host.calls)
		}
	}
}

func TestIdleCheckProbeSchedulesWhenNoTimer(t *testing.T) {
	host := &fakeHost{
		usageResponse: pluginapi.HTTPResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_after_seconds":120}}}`),
		},
	}
	state := newPluginState(host)
	var timer *fakeTimer
	state.timerFactory = func(d time.Duration, f func()) stoppableTimer {
		timer = &fakeTimer{fire: f}
		return timer
	}
	state.mu.Lock()
	state.cfg.IdleCheckMode = "direct_codex"
	cfg := state.cfg
	state.mu.Unlock()

	result := state.runIdleCheck(cfg)
	if result.Checked != 1 || result.Skipped != 0 || result.Failed != 0 || result.ProbeScheduled != 1 || result.Warmed != 0 {
		t.Fatalf("idle check result = %#v, want one checked probe-scheduled auth", result)
	}
	if timer == nil {
		t.Fatal("timer was not created")
	}
	state.mu.Lock()
	entry := state.timers["idx-1"]
	_, stored := state.results["idx-1"]
	state.mu.Unlock()
	if entry == nil || entry.Window != "5h" || entry.AuthID != "auth-runtime" {
		t.Fatalf("timer entry = %#v, want 5h for auth-runtime", entry)
	}
	if stored {
		t.Fatal("probe result was stored as warmup result")
	}
	for _, call := range host.calls {
		if call.method == "host.http.do" {
			req, ok := call.payload.(pluginapi.HTTPRequest)
			if !ok {
				t.Fatalf("host.http.do payload = %T, want pluginapi.HTTPRequest", call.payload)
			}
			if req.URL == codexUsageProbeURL {
				if req.Method != http.MethodGet || req.Headers.Get("User-Agent") != codexUsageProbeUserAgent || req.Headers.Get("Chatgpt-Account-Id") != "acct-1" {
					t.Fatalf("usage probe request = %#v, want GET with Codex CLI headers", req)
				}
			}
		}
		if call.method == "host.model.execute" {
			t.Fatalf("idle check warmed despite successful probe; calls = %#v", host.calls)
		}
	}
}

func TestIdleCheckProbeFailureFallsBackToWarmup(t *testing.T) {
	host := &fakeHost{
		httpError: errors.New("probe unavailable"),
	}
	state := newPluginState(host)
	var timer *fakeTimer
	state.timerFactory = func(d time.Duration, f func()) stoppableTimer {
		timer = &fakeTimer{fire: f}
		return timer
	}
	state.mu.Lock()
	state.cfg.IdleCheckMode = "host_model"
	state.cfg.WarmupModel = "gpt-5.4-mini"
	cfg := state.cfg
	state.mu.Unlock()

	result := state.runIdleCheck(cfg)
	if result.Checked != 1 || result.Skipped != 0 || result.Failed != 0 || result.ProbeFailed != 1 || result.Warmed != 1 {
		t.Fatalf("idle check result = %#v, want probe failure with successful fallback warmup", result)
	}
	state.mu.Lock()
	stored := state.results["idx-1"]
	state.mu.Unlock()
	if stored.StatusCode != http.StatusOK || stored.Error != "" {
		t.Fatalf("stored result = %#v, want successful fallback warmup", stored)
	}
}

func TestIdleCheckProbeWithoutBoundaryFallsBackToWarmupAndSchedulesFromResponse(t *testing.T) {
	host := &fakeHost{
		usageResponse: pluginapi.HTTPResponse{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"rate_limit":{"allowed":true,"limit_reached":false}}`),
		},
		httpResponse: pluginapi.HTTPResponse{
			StatusCode: http.StatusTooManyRequests,
			Body:       []byte(`{"error":{"type":"usage_limit_reached","message":"limit","resets_in_seconds":120}}`),
		},
	}
	state := newPluginState(host)
	var timer *fakeTimer
	state.timerFactory = func(d time.Duration, f func()) stoppableTimer {
		timer = &fakeTimer{fire: f}
		return timer
	}
	state.mu.Lock()
	state.cfg.IdleCheckMode = "direct_codex"
	state.cfg.WarmupModel = "gpt-5.4-mini"
	cfg := state.cfg
	state.mu.Unlock()

	result := state.runIdleCheck(cfg)
	if result.Checked != 1 || result.Skipped != 0 || result.Failed != 1 || result.ProbeNoBoundary != 1 || result.Warmed != 1 {
		t.Fatalf("idle check result = %#v, want no-boundary probe with failed fallback warmup", result)
	}
	if timer == nil {
		t.Fatal("timer was not created")
	}
	state.mu.Lock()
	entry := state.timers["idx-1"]
	stored := state.results["idx-1"]
	state.mu.Unlock()
	if entry == nil || entry.Window != "usage_limit_reached" || entry.AuthID != "auth-runtime" {
		t.Fatalf("timer entry = %#v, want usage_limit_reached for auth-runtime", entry)
	}
	if stored.StatusCode != http.StatusTooManyRequests || !strings.Contains(stored.Error, "limit") {
		t.Fatalf("stored result = %#v, want 429 limit error", stored)
	}
}

func TestTimerWarmupSchedulesSuccessorFromResponse(t *testing.T) {
	host := &fakeHost{
		httpResponse: pluginapi.HTTPResponse{
			StatusCode: http.StatusTooManyRequests,
			Body:       []byte(`{"error":{"type":"usage_limit_reached","message":"limit","resets_in_seconds":120}}`),
		},
	}
	state := newPluginState(host)
	var timers []*fakeTimer
	state.timerFactory = func(d time.Duration, f func()) stoppableTimer {
		timer := &fakeTimer{fire: f}
		timers = append(timers, timer)
		return timer
	}
	now := time.Unix(1000, 0)
	record := pluginapi.UsageRecord{
		Provider:  "codex",
		AuthIndex: "idx-1",
		AuthID:    "auth-1",
		ResponseHeaders: http.Header{
			"X-Codex-Primary-Window-Minutes":      []string{"300"},
			"X-Codex-Primary-Reset-After-Seconds": []string{"1"},
		},
	}
	if !state.handleUsageRecord(record, now) {
		t.Fatal("handleUsageRecord() did not register initial timer")
	}
	timers[0].fire()
	if len(timers) != 2 {
		t.Fatalf("timers created = %d, want successor timer", len(timers))
	}
	state.mu.Lock()
	entry := state.timers["idx-1"]
	state.mu.Unlock()
	if entry == nil || entry.Window != "usage_limit_reached" {
		t.Fatalf("timer entry = %#v, want successor usage_limit_reached timer", entry)
	}
}

func TestConfigureSchedulesIdleCheck(t *testing.T) {
	state := newPluginState(nil)
	var timer *fakeTimer
	var delays []time.Duration
	state.timerFactory = func(d time.Duration, f func()) stoppableTimer {
		delays = append(delays, d)
		timer = &fakeTimer{fire: f}
		return timer
	}
	rawReq, errMarshal := json.Marshal(lifecycleRequest{ConfigYAML: []byte("idle_check_interval_minutes: 2\n")})
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	if errConfigure := state.configure(rawReq); errConfigure != nil {
		t.Fatalf("configure() error = %v", errConfigure)
	}
	if timer == nil || len(delays) != 1 || delays[0] != time.Minute {
		t.Fatalf("idle check timer = %#v delays = %v, want startup 1m", timer, delays)
	}
	state.mu.Lock()
	next := state.idleCheckNextAt
	state.mu.Unlock()
	if next.IsZero() {
		t.Fatal("idleCheckNextAt was not set")
	}
	timer.fire()
	if len(delays) != 2 || delays[1] != 2*time.Minute {
		t.Fatalf("idle check delays after fire = %v, want recurring 2m", delays)
	}
}

func TestManagementRegisterIncludesResourceTabAndWarmupPOSTRoute(t *testing.T) {
	state := newPluginState(nil)
	rawResp, errRegister := state.handleMethod("management.register", nil)
	if errRegister != nil {
		t.Fatalf("management.register error = %v", errRegister)
	}
	var env envelope
	if errUnmarshal := json.Unmarshal(rawResp, &env); errUnmarshal != nil {
		t.Fatalf("decode envelope: %v", errUnmarshal)
	}
	if !env.OK {
		t.Fatalf("envelope = %#v, want ok", env)
	}
	var reg managementRegistration
	if errUnmarshal := json.Unmarshal(env.Result, &reg); errUnmarshal != nil {
		t.Fatalf("decode registration: %v", errUnmarshal)
	}
	if len(reg.Resources) != 1 || reg.Resources[0].Path != resourcePath || reg.Resources[0].Menu != "Codex Reset Warmup" {
		t.Fatalf("resources = %#v, want status tab", reg.Resources)
	}
	if len(reg.Routes) != 1 || reg.Routes[0].Method != http.MethodPost || reg.Routes[0].Path != managementWarmupPath {
		t.Fatalf("routes = %#v, want POST warmup route", reg.Routes)
	}
}

func TestHandleManagementManualWarmupPOSTRedirectsToStatusTab(t *testing.T) {
	host := &fakeHost{}
	state := newPluginState(host)
	req := managementRequest{
		Method: http.MethodPost,
		Path:   warmupActionPath,
		Headers: http.Header{
			"Content-Type": []string{"application/x-www-form-urlencoded"},
		},
		Body: []byte("auth_index=idx-1&theme=dark"),
	}
	rawReq, errMarshal := json.Marshal(req)
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	rawResp, errHandle := state.handleManagement(rawReq)
	if errHandle != nil {
		t.Fatalf("handleManagement() error = %v", errHandle)
	}
	var env envelope
	if errUnmarshal := json.Unmarshal(rawResp, &env); errUnmarshal != nil {
		t.Fatalf("decode envelope: %v", errUnmarshal)
	}
	if !env.OK {
		t.Fatalf("envelope = %#v, want ok", env)
	}
	var resp managementResponse
	if errUnmarshal := json.Unmarshal(env.Result, &resp); errUnmarshal != nil {
		t.Fatalf("decode management response: %v", errUnmarshal)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	location := resp.Headers.Get("Location")
	if !strings.HasPrefix(location, resourceRelativePath+"?") || !strings.Contains(location, "notice=warmup_ok") || !strings.Contains(location, "auth_index=idx-1") || !strings.Contains(location, "theme=dark") {
		t.Fatalf("Location = %q, want status tab success redirect", location)
	}
}

func TestRenderStatusPageIncludesDashboardSectionsAndPOSTForm(t *testing.T) {
	state := newPluginState(nil)
	page := string(state.renderStatusPage([]pluginapi.HostAuthFileEntry{{
		AuthIndex: "idx-1",
		Provider:  "codex",
		Name:      "codex-a.json",
	}}, "", "", "dark"))
	for _, want := range []string{"Operational Summary", "Manual Warmup", "Timers", "Recent Warmups", "Runtime Settings"} {
		if !strings.Contains(page, want) {
			t.Fatalf("status page missing %q:\n%s", want, page)
		}
	}
	if !strings.Contains(page, `method="post"`) || !strings.Contains(page, warmupRelativePath) || !strings.Contains(page, `name="auth_index" value="idx-1"`) {
		t.Fatalf("status page missing manual warmup POST form:\n%s", page)
	}
	if !strings.Contains(page, `data-warmup-form`) || !strings.Contains(page, `"X-Management-Key"`) || !strings.Contains(page, `codex-reset-warmup-management-key`) {
		t.Fatalf("status page missing authenticated warmup submit script:\n%s", page)
	}
	if strings.Contains(page, `action="/v0/management`) {
		t.Fatalf("status page contains root-absolute warmup form action:\n%s", page)
	}
	if !strings.Contains(page, `<code>codex-a.json</code>`) {
		t.Fatalf("status page missing code-styled auth name:\n%s", page)
	}
	if !strings.Contains(page, `<html data-theme="dark">`) || !strings.Contains(page, `name="theme" value="dark"`) || !strings.Contains(page, "prefers-color-scheme:dark") {
		t.Fatalf("status page missing theme support:\n%s", page)
	}
	if strings.Contains(page, "action=warmup") {
		t.Fatalf("status page still contains old GET warmup action:\n%s", page)
	}
}

func TestManagementBrowserPathsPreserveReverseProxyPrefix(t *testing.T) {
	pageURL, errParsePageURL := url.Parse("https://example.test/cpa/v0/resource/plugins/codex-reset-warmup/status")
	if errParsePageURL != nil {
		t.Fatal(errParsePageURL)
	}
	actionURL, errParseActionURL := url.Parse(warmupRelativePath)
	if errParseActionURL != nil {
		t.Fatal(errParseActionURL)
	}
	if got := pageURL.ResolveReference(actionURL).String(); got != "https://example.test/cpa/v0/management/plugins/codex-reset-warmup/warmup" {
		t.Fatalf("resolved warmup action = %q", got)
	}

	postURL, errParsePostURL := url.Parse("https://example.test/cpa/v0/management/plugins/codex-reset-warmup/warmup")
	if errParsePostURL != nil {
		t.Fatal(errParsePostURL)
	}
	redirectURL, errParseRedirectURL := url.Parse(manualWarmupRedirect("warmup_ok", "idx-1", "200", "", ""))
	if errParseRedirectURL != nil {
		t.Fatal(errParseRedirectURL)
	}
	if got := postURL.ResolveReference(redirectURL).String(); got != "https://example.test/cpa/v0/resource/plugins/codex-reset-warmup/status?auth_index=idx-1&notice=warmup_ok&status=200" {
		t.Fatalf("resolved warmup redirect = %q", got)
	}
}

func TestFormatDisplayTime(t *testing.T) {
	zone := time.FixedZone("TST", -5*60*60)
	got := formatDisplayTime(time.Date(2026, time.July, 5, 23, 2, 27, 0, zone))
	if got != "Jul 5, 2026 11:02:27 PM TST" {
		t.Fatalf("formatDisplayTime() = %q", got)
	}
	if zero := formatDisplayTime(time.Time{}); zero != "" {
		t.Fatalf("formatDisplayTime(zero) = %q, want empty", zero)
	}
}

func TestRecentWarmupHealth(t *testing.T) {
	noData := recentWarmupHealth(nil)
	if noData.label != "No data" || noData.class != "neutral" {
		t.Fatalf("no data health = %#v", noData)
	}
	healthy := recentWarmupHealth([]warmupResult{{RanAt: time.Unix(1000, 0), StatusCode: http.StatusOK}})
	if healthy.label != "Healthy" || healthy.class != "ok" {
		t.Fatalf("healthy = %#v", healthy)
	}
	attention := recentWarmupHealth([]warmupResult{{RanAt: time.Unix(1000, 0), StatusCode: http.StatusTooManyRequests, Error: "limit"}})
	if attention.label != "Attention" || attention.class != "warn" {
		t.Fatalf("attention = %#v", attention)
	}
}
