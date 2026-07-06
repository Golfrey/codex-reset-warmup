# Direct Codex usage probe

Idle checks will try a direct Codex Usage Probe for Untimed Auths before sending a Warmup Request. The plugin will use host-provided Codex auth material and call Codex's usage endpoint directly, rather than routing through CLIProxyAPI's Management API, because the plugin already runs inside CPA and should not require a CPA base URL or management API key to inspect one auth's reset-window state.

A valid future reset boundary from the probe is sufficient to register a Reset Timer, even when reported usage is zero. If the probe returns no eligible reset boundary, the auth is treated as unused or unknown and the plugin falls back to a Warmup Request.

Probe failures are also treated as fallback conditions. An idle check should only be marked failed if the Warmup Request fallback fails too.

After any Warmup Request, the plugin should try to schedule the next Reset Timer from the Warmup Response when the transport exposes usable headers or body data. Timer-fired warmups and idle-check fallback warmups should therefore self-renew when Codex returns reset-window information.

Manual Warmup Actions remain explicit Warmup Requests. Probe-first behavior applies to automatic idle checks for Untimed Auths, not to user-triggered warmups from the management page.

The Codex Usage Probe will hardcode the known Codex usage endpoint and CLI-like user-agent for v1. If that integration stops returning usable reset information, the existing Warmup Request fallback remains the operational recovery path rather than exposing new plugin configuration immediately.

When a Reset Timer fires, the plugin will clear the expired timer before sending the Warmup Request. If the Warmup Response contains usable reset-window information, the plugin will immediately register the successor Reset Timer from that response.

For v1, the Usage Probe will only schedule from the same reset windows the plugin already understands: primary/five-hour windows when `schedule_five_hour` is enabled, and secondary/weekly windows when `schedule_weekly` is enabled. Monthly, additional, or model-specific limits are ignored until they have explicit configuration and UI semantics.

Idle-check reporting should distinguish auths scheduled from a Usage Probe from auths that required a Warmup Request when the existing result model has room for that detail. This is diagnostic information, not a new primary management-page health state.

When multiple eligible reset windows are present, the plugin will use the earliest future reset boundary. This keeps Codex Usage Probe scheduling aligned with the existing Warmup Response parser: configuration controls which windows are eligible, and time ordering selects the Reset Timer.

Usage Probes run during idle checks only. Startup will not proactively probe Untimed Auths before the first idle-check interval.

Probe-first behavior applies to every enabled Codex auth considered by an idle check, but probe calls should follow the existing idle-check iteration model and remain sequential for v1. This avoids a burst of direct Codex usage calls while still covering each Untimed Auth.

A successful Usage Probe should not be stored as a Warmup Response or overwrite Recent Warmup Health. Probe scheduling belongs in idle-check diagnostics and logs; stored warmup history remains about actual Warmup Requests.

The management tab should show the resulting Reset Timer, not promote whether it came from a Usage Probe or Warmup Response. Source attribution remains diagnostic detail for logs and idle-check reporting.

A probe response with `limit_reached: true` still schedules a Reset Timer when it includes an eligible future reset boundary. The Warmup Request fallback is for missing, unusable, or failed probe data, not for limit-reached usage status by itself.

When reading usage windows, the plugin should prefer `reset_at` as the reset boundary. If `reset_at` is absent, it should fall back to `now + reset_after_seconds`. Windows that cannot produce a future reset boundary are ignored.

If a probe returns only windows that are disabled by plugin configuration, it counts as no eligible reset boundary and falls back to a Warmup Request. The probe must create an eligible Reset Timer to suppress warmup.

Idle checks should consider all Codex auth files for Reset Timer registration, not only auths that are otherwise known to be warmup-eligible. Non-Codex auth files remain outside the plugin's scope because the Codex Usage Probe and reset-window semantics are Codex-specific.

If a Codex auth file has enough material for a successful Usage Probe, the plugin should register the Reset Timer even if the auth might later fail runtime scheduler selection for a Warmup Request. Timer registration reflects reset-window knowledge; warmup deliverability is handled when the timer fires.

If a Codex auth file cannot be probed because direct Codex auth material is missing or unusable, the idle check should still attempt the Warmup Request fallback when an `auth_index` is available.

Idle checks do not probe or replace auths that already have a Reset Timer. Existing timers remain authoritative until they fire or are cleared.

The design does not add persisted Usage Probe history in v1. Probe outcomes belong in the last idle-check summary and logs, while persisted warmup history remains limited to Warmup Requests.

When a Usage Probe successfully schedules a Reset Timer during an idle check, that auth counts as checked even though no Warmup Request ran. `Checked` means the idle check handled the auth; `Failed` increments only when both the probe path and Warmup Request fallback fail.
