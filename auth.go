package main

// Host auth lookup, auth-file extraction, and host logging helpers.
import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// listCodexAuths asks the host for auth files and keeps only Codex credentials for this plugin.
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

// getRuntimeAuth returns the sanitized runtime identity used by automatic warmups.
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

// getCodexAuthMaterial reads physical auth JSON only for direct Codex mode.
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
