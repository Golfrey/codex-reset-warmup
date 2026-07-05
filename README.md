# Codex Reset Warmup

Warmup-only Codex reset scheduler for CLIProxyAPI.

This plugin observes Codex `usage.handle` records, detects 5-hour and weekly reset boundaries from CPA-provided response headers or `usage_limit_reached` failure bodies, and registers one in-memory timer per auth. When the timer fires, it sends a small warmup request through `host.model.execute`.

V1 intentionally does not call CPA `/v0/management/reset-quota` and does not query ChatGPT `wham/usage`. Auto scheduling only uses sanitized runtime auth identity from `usage.handle` or `host.auth.get_runtime`; manual `direct_codex` mode intentionally uses `host.auth.get` for the selected auth.

## Configuration

```yaml
plugins:
  configs:
    codex-reset-warmup:
      enabled: true
      warmup_model: gpt-5.4-mini
      warmup_prompt: ping
      warmup_stream: false
      manual_mode: host_model
      cpa_base_url: http://127.0.0.1:8318
      cpa_api_key: ""
      codex_base_url: https://chatgpt.com/backend-api/codex
      idle_check_enabled: true
      idle_check_mode: direct_codex
      idle_check_interval_minutes: 120
      schedule_five_hour: true
      schedule_weekly: true
```

If a timer already exists for an auth, later usage records for that auth are ignored until the timer fires or the plugin is reloaded.

Auto timer warmups use `host.model.execute`. Manual warmups can use `host_model`, `http`, or `direct_codex`.

`http` posts to the configured local CLIProxyAPI `/v1/chat/completions` endpoint with the plugin's private scheduler headers. `direct_codex` reads the selected auth JSON with `host.auth.get` and posts directly to the Codex `/responses` upstream, bypassing CPA priority-based auth selection.

The idle check is a watchdog for auths that currently have no reset timer. Every `idle_check_interval_minutes`, it lists Codex auths, skips auths that already have timers, and sends a warmup/check request for the remaining auths. In `direct_codex` mode, the response is parsed the same way as manual direct warmup, so reset headers or `usage_limit_reached` bodies can register the next normal reset timer.

## Build

From this directory:

```bash
go test ./...
go build -buildmode=c-shared -o /tmp/codex-reset-warmup.dylib .
rm -f /tmp/codex-reset-warmup.h
```
