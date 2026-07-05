package main

// Scheduler hook used to force a warmup request onto one specific auth.
import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

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

// pickAuthRequest only handles requests carrying this plugin's private header and target auth id.
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

// headerValue compares headers loosely because maps may arrive with different casing.
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
