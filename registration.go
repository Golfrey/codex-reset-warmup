package main

// Host registration metadata and capability advertisement.
import (
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// pluginRegistration tells CLIProxyAPI which hooks and management resources this plugin provides.
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
				{Name: "idle_check_enabled", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Periodically checks idle Codex auths that have no registered reset timer."},
				{Name: "idle_check_mode", Type: pluginapi.ConfigFieldTypeString, Description: "Idle check transport: host_model, http, or direct_codex."},
				{Name: "idle_check_interval_minutes", Type: pluginapi.ConfigFieldTypeInteger, Description: "Minutes between idle checks when no timer is registered."},
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
