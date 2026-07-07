# Codex Reset Warmup

Warmup-only Codex reset scheduler for CLIProxyAPI.

This plugin observes Codex `usage.handle` records, detects 5-hour and weekly reset boundaries from CPA-provided response headers or `usage_limit_reached` failure bodies, and registers one in-memory timer per auth. When the timer fires, it sends a small warmup request through `host.model.execute`.

V1 intentionally does not call CPA `/v0/management/reset-quota`. Idle checks query ChatGPT `wham/usage` directly with host-provided Codex auth material before falling back to warmup. Manual `direct_codex` mode intentionally uses `host.auth.get` for the selected auth.

## Configuration

For a store install, the plugin is designed to work with defaults. If you are
enabling it by editing YAML manually, the plugin-specific config can be as small
as:

```yaml
plugins:
  configs:
    codex-reset-warmup:
      enabled: true
```

Only override settings when you need non-default behavior. For example:

```yaml
plugins:
  configs:
    codex-reset-warmup:
      enabled: true
      warmup_model: gpt-5.4-mini
      idle_check_interval_minutes: 120
```

All plugin options are:

| Option | Default | When to change it |
| --- | --- | --- |
| `enabled` | `true` | Set to `false` to keep the plugin installed but stop scheduling, idle checks, and warmups. |
| `warmup_model` | `gpt-5.4-mini` | Use a different Codex-routable model for automatic and `host_model`/`http` warmup requests. |
| `warmup_prompt` | `ping` | Change the tiny prompt sent by warmup requests. |
| `warmup_stream` | `false` | Set to `true` to use `host.model.execute_stream` for warmups instead of non-streaming `host.model.execute`. |
| `manual_mode` | `host_model` | Controls manual warmups from the Management Center: `host_model`, `http`, or `direct_codex`. |
| `cpa_base_url` | `http://127.0.0.1:8318` | Only used when `manual_mode` or `idle_check_mode` is `http`; point it at the local CLIProxyAPI base URL. |
| `cpa_api_key` | `""` | Required only for `http` mode, because that mode calls CLIProxyAPI's OpenAI-compatible endpoint. |
| `codex_base_url` | `https://chatgpt.com/backend-api/codex` | Only used by `direct_codex` warmups; change it only if the Codex upstream base URL changes. |
| `idle_check_enabled` | `true` | Set to `false` to disable the watchdog for Codex auths that do not currently have a reset timer. |
| `idle_check_mode` | `direct_codex` | Fallback warmup mode for idle checks when the usage probe cannot find a reset boundary: `host_model`, `http`, or `direct_codex`. |
| `idle_check_interval_minutes` | `120` | Controls how often the idle watchdog checks untimed Codex auths after the first startup check. |
| `schedule_five_hour` | `true` | Set to `false` to ignore Codex 5-hour reset windows. |
| `schedule_weekly` | `true` | Set to `false` to ignore Codex weekly reset windows. |

If a timer already exists for an auth, later usage records for that auth are ignored until the timer fires or the plugin is reloaded.

Auto timer warmups use `host.model.execute`. Manual warmups can use `host_model`, `http`, or `direct_codex`.

`host_model` is the default manual warmup mode. `http` posts to the configured local CLIProxyAPI `/v1/chat/completions` endpoint with the plugin's private scheduler headers and requires `cpa_api_key`. `direct_codex` reads the selected auth JSON with `host.auth.get` and posts directly to the Codex `/responses` upstream, bypassing CPA priority-based auth selection. The default idle check mode is `direct_codex`.

The idle check is a watchdog for Codex auths that currently have no reset timer. The first idle check runs one minute after the plugin starts; after that, every `idle_check_interval_minutes`, it lists Codex auths and skips auths that already have timers. For untimed auths, it first queries `https://chatgpt.com/backend-api/wham/usage` directly and registers a timer from the earliest enabled future primary/5-hour or secondary/weekly reset boundary. If the probe fails or returns no eligible reset boundary, the idle check sends a warmup request with `idle_check_mode`.

Warmup responses from timer fires, manual warmups, and idle-check fallbacks are parsed for reset information when headers or failure bodies are available. If a warmup response does not reveal a reset boundary, the plugin immediately runs the direct usage probe for that auth before giving up, so a warmup can still register the next timer when `/wham/usage` exposes the boundary.

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

## Publishing

See `docs/publishing/official-store.md` for the official CLIProxyAPI store publishing plan, release asset requirements, and the draft registry entry.
