package main

// Small data shapes shared by the focused implementation files.
// Keeping them together makes the message formats easier to skim.
import (
	"net/http"
	"net/url"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

// pluginConfig mirrors the YAML block users put under plugins.configs.codex-reset-warmup.
type pluginConfig struct {
	Enabled                  bool   `yaml:"enabled"`
	WarmupModel              string `yaml:"warmup_model"`
	WarmupPrompt             string `yaml:"warmup_prompt"`
	WarmupStream             bool   `yaml:"warmup_stream"`
	ManualMode               string `yaml:"manual_mode"`
	CPABaseURL               string `yaml:"cpa_base_url"`
	CPAAPIKey                string `yaml:"cpa_api_key"`
	CodexBaseURL             string `yaml:"codex_base_url"`
	IdleCheckEnabled         bool   `yaml:"idle_check_enabled"`
	IdleCheckMode            string `yaml:"idle_check_mode"`
	IdleCheckIntervalMinutes int    `yaml:"idle_check_interval_minutes"`
	ScheduleFiveHour         bool   `yaml:"schedule_five_hour"`
	ScheduleWeekly           bool   `yaml:"schedule_weekly"`
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
	Routes    []managementRoute    `json:"routes,omitempty"`
	Resources []managementResource `json:"resources,omitempty"`
}

type managementRoute struct {
	Method      string `json:"Method"`
	Path        string `json:"Path"`
	Description string `json:"Description"`
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

// timerEntry remembers why a warmup is scheduled and which auth should receive it.
type timerEntry struct {
	AuthIndex string    `json:"auth_index"`
	AuthID    string    `json:"auth_id,omitempty"`
	Window    string    `json:"window"`
	ResetAt   time.Time `json:"reset_at"`
	CreatedAt time.Time `json:"created_at"`
	timer     stoppableTimer
}

// warmupResult is what the management page shows after a manual or automatic warmup.
type warmupResult struct {
	AuthIndex  string    `json:"auth_index"`
	AuthID     string    `json:"auth_id,omitempty"`
	RanAt      time.Time `json:"ran_at"`
	StatusCode int       `json:"status_code,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type idleCheckResult struct {
	RanAt   time.Time `json:"ran_at"`
	Checked int       `json:"checked"`
	Skipped int       `json:"skipped"`
	Failed  int       `json:"failed"`
	Error   string    `json:"error,omitempty"`
}

// resetBoundary is the normalized answer to "when should this auth wake up again?"
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

// statusPageSnapshot copies mutable state before rendering HTML, so rendering does not hold the mutex.
type statusPageSnapshot struct {
	cfg         pluginConfig
	idleNextAt  time.Time
	idleRunning bool
	idleLast    idleCheckResult
	timers      []timerEntry
	results     []warmupResult
}
