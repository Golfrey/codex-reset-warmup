package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

const (
	pluginName          = "codex-reset-warmup"
	pluginVersion       = "0.1.0"
	defaultWarmupModel  = "gpt-5.4-mini"
	defaultWarmupPrompt = "ping"
	defaultManualMode   = "host_model"
	defaultCPABaseURL   = "http://127.0.0.1:8318"
	defaultCodexBaseURL = "https://chatgpt.com/backend-api/codex"
	headerSecret        = "X-Codex-Reset-Warmup"
	headerTargetAuthID  = "X-Codex-Reset-Warmup-Auth-Id"
	resourcePath        = "/status"
	resourceContentType = "text/html; charset=utf-8"

	fiveHourMinutes = int64(300)
	weeklyMinutes   = int64(10080)
	fiveHourSeconds = int64(18000)
	weeklySeconds   = int64(604800)
)

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

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type pluginConfig struct {
	Enabled          bool   `yaml:"enabled"`
	WarmupModel      string `yaml:"warmup_model"`
	WarmupPrompt     string `yaml:"warmup_prompt"`
	WarmupStream     bool   `yaml:"warmup_stream"`
	ManualMode       string `yaml:"manual_mode"`
	CPABaseURL       string `yaml:"cpa_base_url"`
	CPAAPIKey        string `yaml:"cpa_api_key"`
	CodexBaseURL     string `yaml:"codex_base_url"`
	ScheduleFiveHour bool   `yaml:"schedule_five_hour"`
	ScheduleWeekly   bool   `yaml:"schedule_weekly"`
}

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	UsagePlugin   bool `json:"usage_plugin"`
	Scheduler     bool `json:"scheduler"`
	ManagementAPI bool `json:"management_api"`
}

type managementRegistration struct {
	Resources []managementResource `json:"resources,omitempty"`
}

type managementResource struct {
	Path        string `json:"Path"`
	Menu        string `json:"Menu"`
	Description string `json:"Description"`
}

type managementRequest struct {
	Method         string
	Path           string
	Headers        http.Header
	Query          url.Values
	Body           []byte
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type managementResponse struct {
	StatusCode int         `json:"StatusCode"`
	Headers    http.Header `json:"Headers"`
	Body       []byte      `json:"Body"`
}

type hostModelExecutionRequest struct {
	pluginapi.HostModelExecutionRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type authListResponse struct {
	Files []pluginapi.HostAuthFileEntry `json:"files"`
}

type codexAuthMaterial struct {
	Token     string
	BaseURL   string
	AccountID string
	IsAPIKey  bool
}

type timerEntry struct {
	AuthIndex string    `json:"auth_index"`
	AuthID    string    `json:"auth_id,omitempty"`
	Window    string    `json:"window"`
	ResetAt   time.Time `json:"reset_at"`
	CreatedAt time.Time `json:"created_at"`
	timer     stoppableTimer
}

type warmupResult struct {
	AuthIndex  string    `json:"auth_index"`
	AuthID     string    `json:"auth_id,omitempty"`
	RanAt      time.Time `json:"ran_at"`
	StatusCode int       `json:"status_code,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type resetBoundary struct {
	AuthIndex string
	AuthID    string
	Window    string
	ResetAt   time.Time
}

type usageFailureBody struct {
	Type            string            `json:"type"`
	ResetsAt        int64             `json:"resets_at"`
	ResetsInSeconds int64             `json:"resets_in_seconds"`
	Error           *usageFailureBody `json:"error"`
}

type pluginState struct {
	host         hostClient
	secret       string
	timerFactory timerFactory
	parseReset   func(pluginapi.UsageRecord, time.Time, pluginConfig) (resetBoundary, bool)

	mu      sync.Mutex
	cfg     pluginConfig
	timers  map[string]*timerEntry
	results map[string]warmupResult
}

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

func defaultConfig() pluginConfig {
	return pluginConfig{
		Enabled:          true,
		WarmupModel:      defaultWarmupModel,
		WarmupPrompt:     defaultWarmupPrompt,
		WarmupStream:     false,
		ManualMode:       defaultManualMode,
		CPABaseURL:       defaultCPABaseURL,
		CodexBaseURL:     defaultCodexBaseURL,
		ScheduleFiveHour: true,
		ScheduleWeekly:   true,
	}
}

func newSecret() string {
	buf := make([]byte, 16)
	if _, errRead := rand.Read(buf); errRead == nil {
		return hex.EncodeToString(buf)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func (s *pluginState) handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		if errConfigure := s.configure(request); errConfigure != nil {
			return nil, errConfigure
		}
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodUsageHandle:
		return s.handleUsage(request)
	case pluginabi.MethodSchedulerPick:
		return s.pickAuth(request)
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegistration{
			Resources: []managementResource{{
				Path:        resourcePath,
				Menu:        "Codex Reset Warmup",
				Description: "Shows Codex reset warmup timers and recent warmup results.",
			}},
		})
	case pluginabi.MethodManagementHandle:
		return s.handleManagement(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

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
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return nil
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             pluginName,
			Version:          pluginVersion,
			Author:           "router-for-me",
			GitHubRepository: "https://github.com/router-for-me/CLIProxyAPI",
			Logo:             "https://raw.githubusercontent.com/router-for-me/CLIProxyAPI/main/docs/logo.png",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "enabled", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Enables Codex reset warmup scheduling."},
				{Name: "warmup_model", Type: pluginapi.ConfigFieldTypeString, Description: "Model used for the warmup request."},
				{Name: "warmup_prompt", Type: pluginapi.ConfigFieldTypeString, Description: "Prompt sent by the warmup request."},
				{Name: "warmup_stream", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Uses host.model.execute_stream for warmup when enabled."},
				{Name: "manual_mode", Type: pluginapi.ConfigFieldTypeString, Description: "Manual warmup transport: host_model, http, or direct_codex."},
				{Name: "cpa_base_url", Type: pluginapi.ConfigFieldTypeString, Description: "CLIProxyAPI base URL for manual_mode=http."},
				{Name: "cpa_api_key", Type: pluginapi.ConfigFieldTypeString, Description: "CLIProxyAPI API key for manual_mode=http."},
				{Name: "codex_base_url", Type: pluginapi.ConfigFieldTypeString, Description: "Codex upstream base URL for manual_mode=direct_codex."},
				{Name: "schedule_five_hour", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Schedules warmups for Codex 5-hour windows."},
				{Name: "schedule_weekly", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Schedules warmups for Codex weekly windows."},
			},
		},
		Capabilities: registrationCapabilities{
			UsagePlugin:   true,
			Scheduler:     true,
			ManagementAPI: true,
		},
	}
}

func (s *pluginState) handleUsage(raw []byte) ([]byte, error) {
	var record pluginapi.UsageRecord
	if errUnmarshal := json.Unmarshal(raw, &record); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	s.handleUsageRecord(record, time.Now())
	return okEnvelope(map[string]any{})
}

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

func (s *pluginState) runWarmup(entry timerEntry, cfg pluginConfig) warmupResult {
	result := warmupResult{
		AuthIndex: entry.AuthIndex,
		AuthID:    strings.TrimSpace(entry.AuthID),
		RanAt:     time.Now(),
	}
	authID := result.AuthID
	if authID == "" {
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
	switch cfg.ManualMode {
	case "http":
		result = s.runManualHTTPWarmup(entry, cfg)
	case "direct_codex":
		result = s.runManualDirectCodexWarmup(entry, cfg)
	default:
		result = s.runWarmup(entry, cfg)
	}
	s.mu.Lock()
	s.results[authIndex] = result
	s.mu.Unlock()
	return result
}

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
	s.logWarmupResult("info", "codex reset warmup completed", cfg, result)
	return result
}

func (s *pluginState) listCodexAuths() ([]pluginapi.HostAuthFileEntry, error) {
	if s.host == nil {
		return nil, fmt.Errorf("host callbacks unavailable")
	}
	raw, errCall := s.host.Call(pluginabi.MethodHostAuthList, map[string]any{})
	if errCall != nil {
		return nil, errCall
	}
	var resp authListResponse
	if errUnmarshal := json.Unmarshal(raw, &resp); errUnmarshal != nil {
		return nil, fmt.Errorf("decode host.auth.list result: %w", errUnmarshal)
	}
	out := make([]pluginapi.HostAuthFileEntry, 0, len(resp.Files))
	for _, entry := range resp.Files {
		if strings.EqualFold(strings.TrimSpace(entry.Provider), "codex") || strings.EqualFold(strings.TrimSpace(entry.Type), "codex") {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(out[i].Name))
		right := strings.ToLower(strings.TrimSpace(out[j].Name))
		if left == right {
			return strings.TrimSpace(out[i].AuthIndex) < strings.TrimSpace(out[j].AuthIndex)
		}
		return left < right
	})
	return out, nil
}

func (s *pluginState) logWarmupResult(level string, message string, cfg pluginConfig, result warmupResult) {
	fields := map[string]any{
		"auth_index": result.AuthIndex,
		"auth_id":    result.AuthID,
		"model":      cfg.WarmupModel,
	}
	if result.StatusCode > 0 {
		fields["status_code"] = result.StatusCode
	}
	if strings.TrimSpace(result.Error) != "" {
		fields["error"] = result.Error
	}
	s.logHost(level, message, fields)
}

func (s *pluginState) logHost(level string, message string, fields map[string]any) {
	if s.host == nil {
		return
	}
	_, _ = s.host.Call(pluginabi.MethodHostLog, map[string]any{
		"level":   level,
		"message": message,
		"fields":  fields,
	})
}

func (s *pluginState) getRuntimeAuth(authIndex string) (pluginapi.HostAuthFileEntry, error) {
	if s.host == nil {
		return pluginapi.HostAuthFileEntry{}, fmt.Errorf("host callbacks unavailable")
	}
	raw, errCall := s.host.Call(pluginabi.MethodHostAuthGetRuntime, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if errCall != nil {
		return pluginapi.HostAuthFileEntry{}, errCall
	}
	var resp pluginapi.HostAuthGetRuntimeResponse
	if errUnmarshal := json.Unmarshal(raw, &resp); errUnmarshal != nil {
		return pluginapi.HostAuthFileEntry{}, fmt.Errorf("decode host.auth.get_runtime result: %w", errUnmarshal)
	}
	return resp.Auth, nil
}

func (s *pluginState) getCodexAuthMaterial(authIndex string, cfg pluginConfig) (codexAuthMaterial, error) {
	if s.host == nil {
		return codexAuthMaterial{}, fmt.Errorf("host callbacks unavailable")
	}
	raw, errCall := s.host.Call(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if errCall != nil {
		return codexAuthMaterial{}, errCall
	}
	var resp pluginapi.HostAuthGetResponse
	if errUnmarshal := json.Unmarshal(raw, &resp); errUnmarshal != nil {
		return codexAuthMaterial{}, fmt.Errorf("decode host.auth.get result: %w", errUnmarshal)
	}
	accessToken := extractAuthString(resp.JSON, "access_token")
	apiKey := extractAuthString(resp.JSON, "api_key")
	material := codexAuthMaterial{
		Token:     accessToken,
		BaseURL:   extractAuthString(resp.JSON, "base_url"),
		AccountID: extractAuthString(resp.JSON, "account_id"),
		IsAPIKey:  false,
	}
	if material.Token == "" {
		material.Token = apiKey
		material.IsAPIKey = apiKey != ""
	}
	if material.Token == "" {
		material.Token = extractNestedAuthString(resp.JSON, "token", "access_token")
	}
	if material.BaseURL == "" {
		material.BaseURL = cfg.CodexBaseURL
	}
	material.BaseURL = strings.TrimRight(strings.TrimSpace(material.BaseURL), "/")
	if material.Token == "" {
		return codexAuthMaterial{}, fmt.Errorf("codex access token not found in auth file")
	}
	if material.BaseURL == "" {
		return codexAuthMaterial{}, fmt.Errorf("codex base url not configured")
	}
	return material, nil
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

func extractAuthString(raw json.RawMessage, keys ...string) string {
	if len(raw) == 0 {
		return ""
	}
	var parsed map[string]any
	if errUnmarshal := json.Unmarshal(raw, &parsed); errUnmarshal != nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := parsed[key].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func extractNestedAuthString(raw json.RawMessage, parent string, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var parsed map[string]any
	if errUnmarshal := json.Unmarshal(raw, &parsed); errUnmarshal != nil {
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

func (s *pluginState) pickAuth(raw []byte) ([]byte, error) {
	var req pluginapi.SchedulerPickRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	resp, errPick := s.pickAuthRequest(req)
	if errPick != nil {
		return errorEnvelopeStatus("warmup_auth_unavailable", errPick.Error(), http.StatusConflict), nil
	}
	return okEnvelope(resp)
}

func (s *pluginState) pickAuthRequest(req pluginapi.SchedulerPickRequest) (pluginapi.SchedulerPickResponse, error) {
	if headerValue(req.Options.Headers, headerSecret) != s.secret {
		return pluginapi.SchedulerPickResponse{Handled: false}, nil
	}
	targetAuthID := strings.TrimSpace(headerValue(req.Options.Headers, headerTargetAuthID))
	if targetAuthID == "" {
		return pluginapi.SchedulerPickResponse{Handled: false}, nil
	}
	for _, candidate := range req.Candidates {
		if strings.TrimSpace(candidate.ID) == targetAuthID {
			return pluginapi.SchedulerPickResponse{Handled: true, AuthID: targetAuthID}, nil
		}
	}
	return pluginapi.SchedulerPickResponse{}, fmt.Errorf("target auth %s is not selectable for warmup", targetAuthID)
}

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

func headerValue(headers map[string][]string, key string) string {
	canonicalKey := http.CanonicalHeaderKey(key)
	for candidate, values := range headers {
		if !strings.EqualFold(candidate, key) && !strings.EqualFold(http.CanonicalHeaderKey(candidate), canonicalKey) {
			continue
		}
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

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

func (s *pluginState) renderStatusPage(auths []pluginapi.HostAuthFileEntry, notice string, noticeError string) []byte {
	s.mu.Lock()
	cfg := s.cfg
	timers := make([]timerEntry, 0, len(s.timers))
	for _, entry := range s.timers {
		if entry != nil {
			copyEntry := *entry
			copyEntry.timer = nil
			timers = append(timers, copyEntry)
		}
	}
	results := make([]warmupResult, 0, len(s.results))
	for _, result := range s.results {
		results = append(results, result)
	}
	s.mu.Unlock()

	sort.Slice(timers, func(i, j int) bool {
		return timers[i].ResetAt.Before(timers[j].ResetAt)
	})
	sort.Slice(results, func(i, j int) bool {
		return results[i].RanAt.After(results[j].RanAt)
	})

	var out bytes.Buffer
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
	out.WriteString("<dl>")
	writeDefinition(&out, "Enabled", strconv.FormatBool(cfg.Enabled))
	writeDefinition(&out, "Warmup model", cfg.WarmupModel)
	writeDefinition(&out, "Manual mode", cfg.ManualMode)
	if cfg.ManualMode == "http" {
		writeDefinition(&out, "CPA base URL", cfg.CPABaseURL)
	}
	if cfg.ManualMode == "direct_codex" {
		writeDefinition(&out, "Codex base URL", cfg.CodexBaseURL)
	}
	writeDefinition(&out, "5-hour windows", strconv.FormatBool(cfg.ScheduleFiveHour))
	writeDefinition(&out, "Weekly windows", strconv.FormatBool(cfg.ScheduleWeekly))
	out.WriteString("</dl>")

	out.WriteString("<h2>Manual Warmup</h2><table><thead><tr><th>Auth index</th><th>Name</th><th>Status</th><th>Action</th></tr></thead><tbody>")
	if len(auths) == 0 {
		out.WriteString("<tr><td colspan=\"4\">No Codex auths found.</td></tr>")
	}
	for _, auth := range auths {
		authIndex := strings.TrimSpace(auth.AuthIndex)
		out.WriteString("<tr><td><code>")
		out.WriteString(html.EscapeString(authIndex))
		out.WriteString("</code></td><td>")
		out.WriteString(html.EscapeString(auth.Name))
		out.WriteString("</td><td>")
		out.WriteString(html.EscapeString(auth.Status))
		out.WriteString("</td><td>")
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

	out.WriteString("<h2>Timers</h2><table><thead><tr><th>Auth index</th><th>Auth ID</th><th>Window</th><th>Reset at</th></tr></thead><tbody>")
	if len(timers) == 0 {
		out.WriteString("<tr><td colspan=\"4\">No timers registered.</td></tr>")
	}
	for _, entry := range timers {
		out.WriteString("<tr><td><code>")
		out.WriteString(html.EscapeString(entry.AuthIndex))
		out.WriteString("</code></td><td><code>")
		out.WriteString(html.EscapeString(entry.AuthID))
		out.WriteString("</code></td><td>")
		out.WriteString(html.EscapeString(entry.Window))
		out.WriteString("</td><td>")
		out.WriteString(html.EscapeString(entry.ResetAt.Format(time.RFC3339)))
		out.WriteString("</td></tr>")
	}
	out.WriteString("</tbody></table>")

	out.WriteString("<h2>Recent Warmups</h2><table><thead><tr><th>Auth index</th><th>Auth ID</th><th>Ran at</th><th>Status</th><th>Error</th></tr></thead><tbody>")
	if len(results) == 0 {
		out.WriteString("<tr><td colspan=\"5\">No warmups have run.</td></tr>")
	}
	for _, result := range results {
		out.WriteString("<tr><td><code>")
		out.WriteString(html.EscapeString(result.AuthIndex))
		out.WriteString("</code></td><td><code>")
		out.WriteString(html.EscapeString(result.AuthID))
		out.WriteString("</code></td><td>")
		out.WriteString(html.EscapeString(result.RanAt.Format(time.RFC3339)))
		out.WriteString("</td><td>")
		out.WriteString(strconv.Itoa(result.StatusCode))
		out.WriteString("</td><td class=\"error\">")
		out.WriteString(html.EscapeString(result.Error))
		out.WriteString("</td></tr>")
	}
	out.WriteString("</tbody></table></body></html>")
	return out.Bytes()
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

func (s *pluginState) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.timers {
		if entry != nil && entry.timer != nil {
			entry.timer.Stop()
		}
		delete(s.timers, key)
	}
}

func okEnvelope(v any) ([]byte, error) {
	raw, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	return errorEnvelopeStatus(code, message, 0)
}

func errorEnvelopeStatus(code, message string, status int) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message, HTTPStatus: status}})
	return raw
}
