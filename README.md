# Codex Reset Warmup

Warmup-only Codex reset scheduler for CLIProxyAPI.

This plugin observes Codex `usage.handle` records, detects 5-hour and weekly reset boundaries from CPA-provided response headers or `usage_limit_reached` failure bodies, and registers one in-memory timer per auth. When the timer fires, it sends a small warmup request through `host.model.execute`.

V1 intentionally does not call CPA `/v0/management/reset-quota`. Idle checks query ChatGPT `wham/usage` directly with host-provided Codex auth material before falling back to warmup. Manual `direct_codex` mode intentionally uses `host.auth.get` for the selected auth.

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

The idle check is a watchdog for Codex auths that currently have no reset timer. The first idle check runs one minute after the plugin starts; after that, every `idle_check_interval_minutes`, it lists Codex auths and skips auths that already have timers. For untimed auths, it first queries `https://chatgpt.com/backend-api/wham/usage` directly and registers a timer from the earliest enabled future primary/5-hour or secondary/weekly reset boundary. If the probe fails or returns no eligible reset boundary, the idle check sends a warmup request with `idle_check_mode`.

Warmup responses from timer fires, manual warmups, and idle-check fallbacks are parsed for reset information when headers or failure bodies are available, so a warmup can immediately register the next timer.

## Code Layout

The plugin implementation is split by responsibility so each file has one main job:

- `plugin.go` contains shared constants and the host method dispatcher.
- `state.go`, `types.go`, and `config.go` define in-memory state, message shapes, and config normalization.
- `usage.go`, `usage_probe.go`, `reset_parse.go`, and `idle_check.go` turn usage/reset clues into scheduled warmup timers.
- `warmup.go`, `auth.go`, and `scheduler.go` run warmups, fetch auth material, and pin warmup requests to the intended auth.
- `management.go`, `registration.go`, and `envelope.go` handle the management page, plugin registration, and ABI response envelopes.

## Build

From this directory:

```bash
go test ./...
go build -buildmode=c-shared -o /tmp/codex-reset-warmup.dylib .
rm -f /tmp/codex-reset-warmup.h
```
