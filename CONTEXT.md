# Codex Reset Warmup

This context describes the user-facing language for the Codex reset warmup plugin and its management surface.

## Language

**Management Center Tab**:
A browser-visible plugin page that appears as a tab or menu entry inside CLIProxyAPI's Management Center navigation, while the page content is still served by the plugin.
_Avoid_: Embedded React page, Management Center source page

**Plugin Resource Page**:
A standalone browser page owned and rendered by the plugin for viewing or operating plugin-specific state.
_Avoid_: API endpoint, backend handler page

**Management Center Parity**:
The plugin page should match the Management Center's visual language and interaction patterns where possible, while remaining a plugin-owned resource page.
_Avoid_: Exact source reuse, separate product style

**Operational Summary**:
The first page section that summarizes whether warmup is enabled, how many timers are scheduled, whether recent warmups are healthy, and when the next idle check will run.
_Avoid_: Raw configuration dump, landing hero

**Manual Warmup Action**:
A user-triggered warmup for one Codex auth, submitted as an authenticated action rather than ordinary page navigation.
_Avoid_: Warmup link, automatic warmup

**Recent Warmup Health**:
The status derived from the latest stored warmup result: healthy for a successful 2xx result with no error, attention for an error or non-2xx status, and no data when no warmup has run.
_Avoid_: Account health, quota health

**Operational Tab**:
A Management Center Tab focused on current status, manual actions, timers, and recent results, without duplicating plugin configuration editing.
_Avoid_: Config editor, settings page

**Untimed Auth**:
A Codex auth that does not currently have a Reset Timer registered.
_Avoid_: Current without a trigger, unscheduled account

**Usage Probe**:
A lightweight check that attempts to learn current Codex reset information without consuming a Warmup Request.
_Avoid_: Warmup, ping

**Codex Usage Probe**:
A Usage Probe that calls Codex's usage endpoint with the selected Codex auth to retrieve current reset-window information.
_Avoid_: CPA management API call, synthetic warmup

**Warmup Request**:
A request sent to Codex to make an unused or unknown auth reveal reset information and stay ready for future use.
_Avoid_: Usage probe, health check

**Warmup Response**:
The response data from a Warmup Request that may reveal reset-window information for scheduling the next Reset Timer.
_Avoid_: Probe result, timer result

**Reset Timer**:
The scheduled callback for a Codex auth at a known reset boundary.
_Avoid_: Trigger, alarm
