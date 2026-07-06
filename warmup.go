package main

// Warmup execution paths.
// Automatic warmups use host.model.execute; manual/idle paths can also use HTTP or direct Codex.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// runWarmup is the normal automatic path through CLIProxyAPI scheduler/model execution.
func (s *pluginState) runWarmup(entry timerEntry, cfg pluginConfig) warmupResult {
	result := warmupResult{
		AuthIndex: entry.AuthIndex,
		AuthID:    strings.TrimSpace(entry.AuthID),
		RanAt:     time.Now(),
	}
	authID := result.AuthID
	if strings.TrimSpace(entry.AuthIndex) != "" {
		runtimeAuth, errRuntime := s.getRuntimeAuth(entry.AuthIndex)
		if errRuntime != nil {
			result.Error = errRuntime.Error()
			return result
		}
		authID = strings.TrimSpace(runtimeAuth.ID)
		result.AuthID = authID
	}
	if authID == "" {
		result.Error = "runtime auth id not found"
		s.logWarmupResult("error", "codex reset warmup failed", cfg, result)
		return result
	}

	s.logHost("info", "codex reset warmup started", map[string]any{
		"auth_index": result.AuthIndex,
		"auth_id":    authID,
		"window":     entry.Window,
		"model":      cfg.WarmupModel,
	})
	body, errBody := warmupBody(cfg)
	if errBody != nil {
		result.Error = errBody.Error()
		s.logWarmupResult("error", "codex reset warmup failed", cfg, result)
		return result
	}
	resp, errExecute := s.executeWarmup(authID, cfg, body, false)
	if errExecute != nil {
		result.Error = errExecute.Error()
		s.logWarmupResult("error", "codex reset warmup failed", cfg, result)
		return result
	}
	result.StatusCode = resp.StatusCode
	responseEntry := entry
	responseEntry.AuthID = authID
	s.scheduleFromWarmupResponse(responseEntry, cfg, resp, result.RanAt)
	if resp.StatusCode >= 400 {
		result.Error = responseErrorSummary(resp.Body)
	}
	s.logWarmupResult("info", "codex reset warmup completed", cfg, result)
	return result
}

func (s *pluginState) runManualWarmup(authIndex string) warmupResult {
	authIndex = strings.TrimSpace(authIndex)
	result := warmupResult{
		AuthIndex: authIndex,
		RanAt:     time.Now(),
	}
	if authIndex == "" {
		result.Error = "auth_index is required"
		return result
	}
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	if !cfg.Enabled {
		result.Error = "plugin is disabled"
		return result
	}
	runtimeAuth, errRuntime := s.getRuntimeAuth(authIndex)
	if errRuntime != nil {
		result.Error = errRuntime.Error()
		return result
	}
	if !strings.EqualFold(strings.TrimSpace(runtimeAuth.Provider), "codex") {
		result.Error = "auth is not a codex credential"
		return result
	}
	entry := timerEntry{
		AuthIndex: authIndex,
		AuthID:    strings.TrimSpace(runtimeAuth.ID),
		Window:    "manual",
	}
	result = s.runWarmupWithMode(entry, cfg, cfg.ManualMode)
	s.mu.Lock()
	s.results[authIndex] = result
	s.mu.Unlock()
	return result
}

// runWarmupWithMode chooses the transport requested by manual_mode or idle_check_mode.
func (s *pluginState) runWarmupWithMode(entry timerEntry, cfg pluginConfig, mode string) warmupResult {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "http":
		return s.runManualHTTPWarmup(entry, cfg)
	case "direct_codex":
		return s.runManualDirectCodexWarmup(entry, cfg)
	default:
		return s.runWarmup(entry, cfg)
	}
}

// runManualDirectCodexWarmup bypasses CPA routing and calls the selected Codex credential directly.
func (s *pluginState) runManualDirectCodexWarmup(entry timerEntry, cfg pluginConfig) warmupResult {
	result := warmupResult{
		AuthIndex: entry.AuthIndex,
		AuthID:    strings.TrimSpace(entry.AuthID),
		RanAt:     time.Now(),
	}
	material, errAuth := s.getCodexAuthMaterial(entry.AuthIndex, cfg)
	if errAuth != nil {
		result.Error = errAuth.Error()
		s.logWarmupResult("error", "codex reset warmup failed", cfg, result)
		return result
	}
	body, errBody := directCodexWarmupBody(cfg)
	if errBody != nil {
		result.Error = errBody.Error()
		s.logWarmupResult("error", "codex reset warmup failed", cfg, result)
		return result
	}
	resp, errExecute := s.executeDirectCodexWarmup(material, cfg, body)
	if errExecute != nil {
		result.Error = errExecute.Error()
		s.logWarmupResult("error", "codex reset warmup failed", cfg, result)
		return result
	}
	result.StatusCode = resp.StatusCode
	s.scheduleFromWarmupResponse(entry, cfg, resp, result.RanAt)
	if resp.StatusCode >= 400 {
		result.Error = responseErrorSummary(resp.Body)
	}
	s.logWarmupResult("info", "codex reset warmup completed", cfg, result)
	return result
}

// scheduleFromWarmupResponse feeds direct Codex headers/errors back through the same reset parser as normal usage.
func (s *pluginState) scheduleFromWarmupResponse(entry timerEntry, cfg pluginConfig, resp pluginapi.HostModelExecutionResponse, now time.Time) bool {
	record := pluginapi.UsageRecord{
		Provider:        "codex",
		AuthIndex:       strings.TrimSpace(entry.AuthIndex),
		AuthID:          strings.TrimSpace(entry.AuthID),
		Model:           cfg.WarmupModel,
		ResponseHeaders: resp.Headers,
	}
	if resp.StatusCode >= 400 {
		record.Failed = true
		record.Failure = pluginapi.UsageFailure{
			StatusCode: resp.StatusCode,
			Body:       string(resp.Body),
		}
	}
	return s.handleUsageRecord(record, now)
}

func (s *pluginState) runManualHTTPWarmup(entry timerEntry, cfg pluginConfig) warmupResult {
	result := warmupResult{
		AuthIndex: entry.AuthIndex,
		AuthID:    strings.TrimSpace(entry.AuthID),
		RanAt:     time.Now(),
	}
	if result.AuthID == "" {
		result.Error = "runtime auth id not found"
		s.logWarmupResult("error", "codex reset warmup failed", cfg, result)
		return result
	}
	body, errBody := warmupBody(cfg)
	if errBody != nil {
		result.Error = errBody.Error()
		s.logWarmupResult("error", "codex reset warmup failed", cfg, result)
		return result
	}
	resp, errExecute := s.executeWarmup(result.AuthID, cfg, body, true)
	if errExecute != nil {
		result.Error = errExecute.Error()
		s.logWarmupResult("error", "codex reset warmup failed", cfg, result)
		return result
	}
	result.StatusCode = resp.StatusCode
	s.scheduleFromWarmupResponse(entry, cfg, resp, result.RanAt)
	s.logWarmupResult("info", "codex reset warmup completed", cfg, result)
	return result
}

func (s *pluginState) executeWarmup(authID string, cfg pluginConfig, body []byte, viaHTTP bool) (pluginapi.HostModelExecutionResponse, error) {
	if s.host == nil {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("host callbacks unavailable")
	}
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set(headerSecret, s.secret)
	headers.Set(headerTargetAuthID, authID)
	if viaHTTP {
		return s.executeWarmupHTTP(cfg, body, headers)
	}
	if cfg.WarmupStream {
		return s.executeWarmupStream(cfg, body, headers)
	}
	raw, errCall := s.host.Call(pluginabi.MethodHostModelExecute, hostModelExecutionRequest{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: "openai",
			ExitProtocol:  "openai",
			Model:         cfg.WarmupModel,
			Stream:        false,
			Body:          body,
			Headers:       headers,
			Query:         url.Values{},
		},
	})
	if errCall != nil {
		return pluginapi.HostModelExecutionResponse{}, errCall
	}
	var resp pluginapi.HostModelExecutionResponse
	if errUnmarshal := json.Unmarshal(raw, &resp); errUnmarshal != nil {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("decode host.model.execute result: %w", errUnmarshal)
	}
	return resp, nil
}

func (s *pluginState) executeWarmupHTTP(cfg pluginConfig, body []byte, headers http.Header) (pluginapi.HostModelExecutionResponse, error) {
	apiKey := strings.TrimSpace(cfg.CPAAPIKey)
	if apiKey == "" {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("cpa_api_key is required when manual_mode=http")
	}
	headers.Set("Authorization", "Bearer "+apiKey)
	raw, errCall := s.host.Call(pluginabi.MethodHostHTTPDo, pluginapi.HTTPRequest{
		Method:  http.MethodPost,
		URL:     strings.TrimRight(cfg.CPABaseURL, "/") + "/v1/chat/completions",
		Headers: headers,
		Body:    body,
	})
	if errCall != nil {
		return pluginapi.HostModelExecutionResponse{}, errCall
	}
	var resp pluginapi.HTTPResponse
	if errUnmarshal := json.Unmarshal(raw, &resp); errUnmarshal != nil {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("decode host.http.do result: %w", errUnmarshal)
	}
	return pluginapi.HostModelExecutionResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Headers,
		Body:       resp.Body,
	}, nil
}

func (s *pluginState) executeDirectCodexWarmup(material codexAuthMaterial, cfg pluginConfig, body []byte) (pluginapi.HostModelExecutionResponse, error) {
	if s.host == nil {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("host callbacks unavailable")
	}
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("Authorization", "Bearer "+material.Token)
	headers.Set("User-Agent", "codex-tui/0.135.0 (Mac OS 26.5.0; arm64) iTerm.app/3.6.10 (codex-tui; 0.135.0)")
	headers.Set("Accept", "text/event-stream")
	headers.Set("Connection", "Keep-Alive")
	headers.Set("Session_id", newSecret())
	if !material.IsAPIKey {
		headers.Set("Originator", "codex-tui")
		if material.AccountID != "" {
			headers.Set("Chatgpt-Account-Id", material.AccountID)
		}
	}
	raw, errCall := s.host.Call(pluginabi.MethodHostHTTPDo, pluginapi.HTTPRequest{
		Method:  http.MethodPost,
		URL:     strings.TrimRight(material.BaseURL, "/") + "/responses",
		Headers: headers,
		Body:    body,
	})
	if errCall != nil {
		return pluginapi.HostModelExecutionResponse{}, errCall
	}
	var resp pluginapi.HTTPResponse
	if errUnmarshal := json.Unmarshal(raw, &resp); errUnmarshal != nil {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("decode host.http.do result: %w", errUnmarshal)
	}
	return pluginapi.HostModelExecutionResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Headers,
		Body:       resp.Body,
	}, nil
}

// executeWarmupStream drains and closes the host stream so the warmup request fully completes.
func (s *pluginState) executeWarmupStream(cfg pluginConfig, body []byte, headers http.Header) (pluginapi.HostModelExecutionResponse, error) {
	raw, errCall := s.host.Call(pluginabi.MethodHostModelExecuteStream, hostModelExecutionRequest{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: "openai",
			ExitProtocol:  "openai",
			Model:         cfg.WarmupModel,
			Stream:        true,
			Body:          body,
			Headers:       headers,
			Query:         url.Values{},
		},
	})
	if errCall != nil {
		return pluginapi.HostModelExecutionResponse{}, errCall
	}
	var resp pluginapi.HostModelStreamResponse
	if errUnmarshal := json.Unmarshal(raw, &resp); errUnmarshal != nil {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("decode host.model.execute_stream result: %w", errUnmarshal)
	}
	if strings.TrimSpace(resp.StreamID) == "" {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("host.model.execute_stream returned empty stream_id")
	}
	defer func() {
		_, _ = s.host.Call(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: resp.StreamID})
	}()
	for {
		chunkRaw, errRead := s.host.Call(pluginabi.MethodHostModelStreamRead, pluginapi.HostModelStreamReadRequest{StreamID: resp.StreamID})
		if errRead != nil {
			return pluginapi.HostModelExecutionResponse{}, errRead
		}
		var chunk pluginapi.HostModelStreamReadResponse
		if errUnmarshal := json.Unmarshal(chunkRaw, &chunk); errUnmarshal != nil {
			return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("decode host.model.stream_read result: %w", errUnmarshal)
		}
		if strings.TrimSpace(chunk.Error) != "" {
			return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("host model stream error: %s", chunk.Error)
		}
		if chunk.Done {
			return pluginapi.HostModelExecutionResponse{
				StatusCode: resp.StatusCode,
				Headers:    resp.Headers,
			}, nil
		}
	}
}

func warmupBody(cfg pluginConfig) ([]byte, error) {
	raw, errMarshal := json.Marshal(map[string]any{
		"model":  cfg.WarmupModel,
		"stream": cfg.WarmupStream,
		"messages": []map[string]string{{
			"role":    "user",
			"content": cfg.WarmupPrompt,
		}},
	})
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal warmup body: %w", errMarshal)
	}
	return raw, nil
}

func directCodexWarmupBody(cfg pluginConfig) ([]byte, error) {
	raw, errMarshal := json.Marshal(map[string]any{
		"model":               cfg.WarmupModel,
		"instructions":        "",
		"stream":              true,
		"store":               false,
		"parallel_tool_calls": true,
		"include":             []string{"reasoning.encrypted_content"},
		"reasoning": map[string]any{
			"effort":  "medium",
			"summary": "auto",
		},
		"input": []map[string]any{{
			"type": "message",
			"role": "user",
			"content": []map[string]string{{
				"type": "input_text",
				"text": cfg.WarmupPrompt,
			}},
		}},
	})
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal direct codex warmup body: %w", errMarshal)
	}
	return raw, nil
}

// responseErrorSummary keeps management-page errors readable instead of dumping huge bodies.
func responseErrorSummary(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}
	var parsed map[string]any
	if errUnmarshal := json.Unmarshal(body, &parsed); errUnmarshal == nil {
		if message := nestedString(parsed, "error", "message"); message != "" {
			return message
		}
		if message := topLevelString(parsed, "message"); message != "" {
			return message
		}
		if code := nestedString(parsed, "error", "code"); code != "" {
			return code
		}
	}
	const maxSummary = 240
	if len(body) > maxSummary {
		body = body[:maxSummary]
	}
	return string(body)
}

func topLevelString(parsed map[string]any, key string) string {
	if parsed == nil {
		return ""
	}
	value, ok := parsed[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func nestedString(parsed map[string]any, parent string, key string) string {
	if parsed == nil {
		return ""
	}
	nested, ok := parsed[parent].(map[string]any)
	if !ok {
		return ""
	}
	value, ok := nested[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
