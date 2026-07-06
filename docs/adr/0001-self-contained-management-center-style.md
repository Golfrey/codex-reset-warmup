# Self-contained Management Center style

The Codex reset warmup plugin exposes a Management Center Tab, but the page content is still owned and served by the plugin. We will match the Management Center's visual language and interaction patterns with a self-contained CSS layer in the plugin page, rather than depending on the host's downloaded `management.html` asset or Management Center source app, so the plugin remains renderable when the host UI asset changes, is missing, or is disabled.
