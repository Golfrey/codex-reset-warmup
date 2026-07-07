package main

// Package-level constants and the small method dispatcher live here.
// Each branch hands off quickly so the plugin entry point stays easy to scan.
import (
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// Shared defaults and host-facing names used across the plugin.
const (
	pluginName           = "codex-reset-warmup"
	pluginVersion        = "0.1.4"
	defaultWarmupModel   = "gpt-5.4-mini"
	defaultWarmupPrompt  = "ping"
	defaultManualMode    = "host_model"
	defaultIdleCheckMode = "direct_codex"
	defaultCPABaseURL    = "http://127.0.0.1:8318"
	defaultCodexBaseURL  = "https://chatgpt.com/backend-api/codex"
	headerSecret         = "X-Codex-Reset-Warmup"
	headerTargetAuthID   = "X-Codex-Reset-Warmup-Auth-Id"
	resourcePath         = "/status"
	resourceFullPath     = "/v0/resource/plugins/" + pluginName + resourcePath
	resourceRelativePath = "../../../resource/plugins/" + pluginName + resourcePath
	managementWarmupPath = "/plugins/" + pluginName + "/warmup"
	warmupActionPath     = "/v0/management" + managementWarmupPath
	warmupRelativePath   = "../../../management" + managementWarmupPath
	resourceContentType  = "text/html; charset=utf-8"

	defaultIdleCheckIntervalMinutes = 120
	startupIdleCheckDelay           = time.Minute
	fiveHourMinutes                 = int64(300)
	weeklyMinutes                   = int64(10080)
	fiveHourSeconds                 = int64(18000)
	weeklySeconds                   = int64(604800)
)

// handleMethod is the plugin front door: the host sends a method name, and we route it to focused handlers.
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
			Routes: []managementRoute{{
				Method:      "POST",
				Path:        managementWarmupPath,
				Description: "Runs a manual Codex reset warmup for one auth.",
			}},
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
