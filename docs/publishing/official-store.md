# Publishing `codex-reset-warmup` to the Official CLIProxyAPI Store

Researched: 2026-07-06.

## Key Conclusion

Publish this plugin through the official CLIProxyAPI store by:

1. Publishing a public GitHub Release from the plugin repository with correctly named per-platform zip assets and `checksums.txt`.
2. Opening a pull request to `router-for-me/CLIProxyAPI-Plugins-Store` that updates only `registry.json` with a `codex-reset-warmup` entry.

The official store is not an artifact host. Its README says the store is a lightweight official registry, maintains only `registry.json`, and requires binaries, checksums, and release notes to remain in each plugin author's own GitHub repository.[^store-purpose] CLIProxyAPI's source hard-codes that registry as the default official source.[^default-registry]

## Store Model

The current official store README requires `registry.json` to use `schema_version: 1` and lists the registry entry shape: required fields are `id`, `name`, `description`, `author`, and `repository`; optional fields include `version`, `logo`, `homepage`, `license`, and `tags`.[^registry-shape] For this plugin, omit `version` from the store entry so the store does not duplicate release state; CLIProxyAPI discovers the installable version from the latest GitHub Release. The official store README also says `repository` must be exactly `https://github.com/{owner}/{repo}`.[^validation-rules]

CLIProxyAPI source supports two install types, `github-release` and `direct`, but `PluginInstallType` defaults an omitted `install.type` to `github-release`.[^install-types] Because the official store README requires schema v1 and the source rejects direct installs in schema v1, the official-store path for this plugin should be a normal GitHub-release entry with no `install` block.[^schema-direct]

CLIProxyAPI reads the configured registry, parses it as JSON, validates plugin records, and includes the official source automatically before any additional configured store registries.[^fetch-registry] The sample config also states that additional plugin store registries are appended while the built-in official registry is always included.[^config-store-sources]

## Release Requirements

For GitHub-release installs, CLIProxyAPI fetches the latest GitHub release for the plugin repository through `https://api.github.com/repos/{owner}/{repo}/releases/latest`.[^latest-release] A release tag is converted into the plugin version by stripping a leading `v` or `V`; the official store README requires tags in `v<version>` form such as `v0.1.5`.[^release-version] The current plugin version is `0.1.5`, matching `pluginVersion = "0.1.5"` in this repo.[^local-version]

Each release must include one `checksums.txt` asset and one zip asset per supported platform. The official store README specifies asset names as `<id>_<version>_<goos>_<goarch>.zip` plus `checksums.txt`, with the version omitting the leading `v`.[^asset-names] CLIProxyAPI source uses the same archive-name function and selects exactly that archive plus `checksums.txt` from the release assets.[^select-assets]

For `codex-reset-warmup` version `0.1.5`, expected asset names are:

```text
codex-reset-warmup_0.1.5_darwin_arm64.zip
codex-reset-warmup_0.1.5_darwin_amd64.zip
codex-reset-warmup_0.1.5_linux_amd64.zip
checksums.txt
```

Add `linux_arm64` and `windows_amd64` only if those builds are actually produced and verified.

`checksums.txt` must be in sha256sum format, and the installer parses the first field as a 64-character SHA-256 hex digest keyed by filename.[^checksum-readme][^checksum-source] CLIProxyAPI downloads the selected archive and `checksums.txt`, parses the checksums, and rejects the install if the archive checksum does not match.[^install-release]

Each platform zip must contain one dynamic library at the zip root. The official store README names the expected library as `<id>.dylib` on Darwin, `<id>.so` on Linux, and `<id>.dll` on Windows.[^zip-layout] CLIProxyAPI source also accepts a versioned root filename, such as `codex-reset-warmup-v0.1.5.<ext>`, but rejects nested dynamic libraries, mismatched filenames, and multiple dynamic libraries.[^zip-source]

## Plugin-Specific Prerequisites

1. Decide the canonical public GitHub repository URL for the plugin release. The current local remote is `git@github.com:Golfrey/codex-reset-warmup.git`, and the draft registry entry now points to `https://github.com/Golfrey/codex-reset-warmup`; change it if the plugin will be transferred before publication. The official store PR needs one exact `https://github.com/{owner}/{repo}` URL.[^validation-rules][^draft-entry]
2. Confirm store metadata:
   - `id`: `codex-reset-warmup`, from `pluginName`.[^local-version]
   - `name`: `Codex Reset Warmup`, matching the management resource menu and README title.[^management-resource][^readme-summary]
   - `author`: current plugin registration uses `router-for-me`.[^registration-metadata]
   - Do not include `version`; the latest GitHub Release tag is the source of release truth.[^release-version][^latest-release]
   - `license`: `MIT`, matching this repo's `LICENSE`.[^local-license]
   - `tags`: suggested `Management`, `Scheduler`, `Codex`, `Warmup`.
3. Build and package release archives from a clean commit. This repo's README already documents the Go build mode for Darwin as `go build -buildmode=c-shared -o /tmp/codex-reset-warmup.dylib .`; CI should run the same build mode for each target platform and delete the generated `.h` file before zipping only the dynamic library.[^local-build]
4. Validate every archive before creating the store PR:
   - Archive filename matches `codex-reset-warmup_0.1.5_<goos>_<goarch>.zip`.
   - The archive root contains only one target dynamic library with an accepted name.
   - `checksums.txt` contains the SHA-256 for every zip.
   - The GitHub release is public and tagged `v0.1.5`.
5. Open a PR to `router-for-me/CLIProxyAPI-Plugins-Store` that updates only `registry.json` unless store documentation needs clarification. The official README says the PR should include the plugin GitHub repository URL, latest release tag, evidence that the required zip asset and `checksums.txt` exist, and a short description of the plugin capability.[^adding-plugin]

## Implemented Support Files

This repo now includes:

- `scripts/package-release.sh`, which builds the current platform with `go build -buildmode=c-shared`, writes a correctly named zip, and updates `dist/checksums.txt`.
- `docs/publishing/registry-entry.json`, which is the current official-store registry-entry draft.
- `.gitignore`, which excludes generated `dist/` files and C headers.

## Draft Registry Entry

Use this as the starting entry after replacing `<owner>/<repo>` with the final public repository:

```json
{
  "id": "codex-reset-warmup",
  "name": "Codex Reset Warmup",
  "description": "Schedules lightweight Codex warmup requests at known reset boundaries so CLIProxyAPI Codex auths stay ready after 5-hour and weekly resets.",
  "author": "router-for-me",
  "repository": "https://github.com/<owner>/<repo>",
  "homepage": "https://github.com/<owner>/<repo>",
  "license": "MIT",
  "tags": [
    "Management",
    "Scheduler",
    "Codex",
    "Warmup"
  ]
}
```

Do not include `"install": {"type": "github-release"}` for the official store PR unless the store maintainers explicitly allow it in schema v1. CLIProxyAPI defaults omitted install type to GitHub-release, and the official store README's schema v1 example does not include an `install` block.[^install-types][^registry-shape]

## Open Questions

- Which GitHub repository should be authoritative for releases: `Golfrey/codex-reset-warmup`, `router-for-me/cpa-plugin-codex-reset-warmup`, or another repo?
- Which platforms should be included in the first release? Darwin arm64, Darwin amd64, and Linux amd64 are a reasonable first set; Windows should wait until the release workflow proves that build can be produced and installed.
- Should `registration.go` metadata be updated before release so `GitHubRepository` points at the plugin's release repository rather than the main CLIProxyAPI repo? This is not a store requirement, but it affects plugin metadata shown by the host.[^registration-metadata]

[^store-purpose]: [CLIProxyAPI-Plugins-Store README, lines 3-5](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/dc69bba9d570705ae0305940c6adb2575f27eaad/README.md#L3-L5).
[^registry-shape]: [CLIProxyAPI-Plugins-Store README, lines 7-45](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/dc69bba9d570705ae0305940c6adb2575f27eaad/README.md#L7-L45).
[^validation-rules]: [CLIProxyAPI-Plugins-Store README, lines 47-55](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/dc69bba9d570705ae0305940c6adb2575f27eaad/README.md#L47-L55).
[^release-version]: [CLIProxyAPI-Plugins-Store README, lines 57-63](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/dc69bba9d570705ae0305940c6adb2575f27eaad/README.md#L57-L63) and [CLIProxyAPI `github.go`, lines 106-113](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/github.go#L106-L113).
[^asset-names]: [CLIProxyAPI-Plugins-Store README, lines 65-80](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/dc69bba9d570705ae0305940c6adb2575f27eaad/README.md#L65-L80).
[^checksum-readme]: [CLIProxyAPI-Plugins-Store README, lines 82-86](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/dc69bba9d570705ae0305940c6adb2575f27eaad/README.md#L82-L86).
[^zip-layout]: [CLIProxyAPI-Plugins-Store README, lines 88-97](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/dc69bba9d570705ae0305940c6adb2575f27eaad/README.md#L88-L97).
[^adding-plugin]: [CLIProxyAPI-Plugins-Store README, lines 99-107](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store/blob/dc69bba9d570705ae0305940c6adb2575f27eaad/README.md#L99-L107).
[^default-registry]: [CLIProxyAPI `registry.go`, lines 14-23](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/registry.go#L14-L23).
[^install-types]: [CLIProxyAPI `registry.go`, lines 21-22](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/registry.go#L21-L22) and [lines 261-267](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/registry.go#L261-L267).
[^schema-direct]: [CLIProxyAPI `registry.go`, lines 166-174](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/registry.go#L166-L174).
[^fetch-registry]: [CLIProxyAPI `github.go`, lines 40-53](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/github.go#L40-L53) and [CLIProxyAPI `registry.go`, lines 126-136](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/registry.go#L126-L136).
[^config-store-sources]: [CLIProxyAPI `config.example.yaml`, lines 61-73](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/config.example.yaml#L61-L73).
[^latest-release]: [CLIProxyAPI `github.go`, lines 56-76](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/github.go#L56-L76).
[^select-assets]: [CLIProxyAPI `github.go`, lines 267-296](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/github.go#L267-L296).
[^checksum-source]: [CLIProxyAPI `checksum.go`, lines 10-45](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/checksum.go#L10-L45).
[^install-release]: [CLIProxyAPI `install.go`, lines 116-143](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/install.go#L116-L143).
[^zip-source]: [CLIProxyAPI `install.go`, lines 318-348](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/install.go#L318-L348) and [lines 371-399](https://github.com/router-for-me/CLIProxyAPI/blob/4a6583e9aa6df4c4555f7c4d5d2671875e921967/internal/pluginstore/install.go#L371-L399).
[^local-version]: [`plugin.go`, lines 11-14](../../plugin.go#L11-L14).
[^management-resource]: [`plugin.go`, lines 49-60](../../plugin.go#L49-L60).
[^registration-metadata]: [`registration.go`, lines 13-19](../../registration.go#L13-L19).
[^readme-summary]: [`README.md`, lines 1-7](../../README.md#L1-L7).
[^local-build]: [`README.md`, lines 50-58](../../README.md#L50-L58).
[^draft-entry]: [`docs/publishing/registry-entry.json`, lines 1-19](registry-entry.json#L1-L19).
[^local-license]: [`LICENSE`, lines 1-21](../../LICENSE#L1-L21).
