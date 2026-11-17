# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Release branches are named `<major>.<minor>.x`.

## [Unreleased]

### Fixed
- Auto-detection no longer lists Drupal modules, themes and profiles (composer `type: drupal-*`) that merely require a `drupal/core` version.

## [1.0.6] - 2026-11-17

### Added
- `scan_depth` (Settings → Projects): how many folder levels below a base folder are searched for projects, default 1. Use 2 for `<base>/<customer>/<project>`. A folder that is a project is not searched further.

### Changed
- Auto-detection only picks up Drupal projects: a `composer.json` requiring `drupal/core*` or `drupal/cms`. A folder with just `.ddev/` or a non-Drupal `composer.json` is no longer listed (add it via `project_paths` if needed).

## [1.0.5] - 2026-11-16

### Added
- Branch defaults for all projects (`defaults:` in config.yaml, "Branch defaults" in desktop Settings): start branch, final branch pattern, merge request target, commit message. Order: repo `drupal-updater.yaml`, then the project override, then these defaults, then the built-in ones.
- The TUI now does everything the desktop app does:
  - merge requests on the confirm screen after renaming (`m` for the selected project, `M` for all, `x` cancels);
  - settings inside the TUI (`S` on the project list; `--config` still opens them on their own) with the same sections and settings as the desktop: excluded projects, update mode, branch defaults, all Claude settings (allowed tools and prompt in a multi-line editor), per-project Claude instructions, and `w` to save a project's overrides to the repo. Project values show what a run uses and which ones come from the repo file;
  - auto-update (`U`), the version in the header, a "repo config" marker in the project list, and the update mode toggle (`m`) is saved like on the desktop.

### Fixed
- "Save to repo" no longer wipes settings already in a project's `drupal-updater.yaml` that are not set locally; it refuses to touch a file that does not parse.

### Changed
- Desktop Settings regrouped into Projects, Branch defaults, Claude (allowed tools and prompt folded under Advanced) and Project overrides.
- Dynamate spark logo, cream on navy as on dynamate.be, in the desktop header and as the app icon.

## [1.0.4] - 2026-11-16

v1.0.3 was tagged but never released (its macOS build failed); its changes ship here.

### Added
- Team-shared project settings: a `drupal-updater.yaml` (YAML or JSON body) committed in a project's repo wins per field over the local `projects:` entry, which only fills the gaps. The desktop Settings screen can write it ("Save to repo") and marks projects that have one.
- Merge requests from the desktop confirm screen: after the rename, "Create MR" (or "Create all MRs") commits the run's changes, pushes the branch and opens a GitLab merge request with `glab` into `git_merge_target` (default: the start branch), with the run summary as description. A conflict with the target is merged in and resolved by a Claude session first; if that fails the merge is aborted. New per-project settings `git_merge_target` and `git_commit_message` (default `Drupal updates {date}`).

- Auto-update for the desktop app: a daily check of GitHub releases shows a banner; "Update & restart" downloads the macOS arm64 DMG or Linux tarball, verifies its ed25519 signature (`<asset>.sig`, signed in CI with the `UPDATE_SIGNING_KEY` secret) and replaces the app. Refused while runs or merge requests are busy. The version is stamped at build time (`VERSION=v1.2.3 scripts/build-dmg.sh`); local builds are `dev` and never update.

### Fixed
- macOS release build runs on `macos-latest`; the `macos-14` runners are retired.

### Changed
- Desktop app restyled in the Dynamate brand (Syne headings, navy/cream palette, light and dark theme).

## [1.0.2] - 2026-10-29

### Added
- Multiple project folders: `base_folders` (each scanned one level deep) and `project_paths` (single projects anywhere). The old `base_folder` key is migrated. Projects with the same folder name are shown as `<parent>/<folder>`.

### Changed
- Final branch names include the date: the default pattern is `hotfix/{date}-drupal-updates` (`{date}` = YYYY-MM-DD, `*` still the project slug). A name that already exists gets `-2`, `-3`, ... Projects still on the old default `hotfix/*` get the new one.

### Removed
- Classic mode (shell commands with retries and auto-healing). Claude is the only runner; the `mode`, `commands`, `default_commands`, `max_attempts` and `command_timeout_minutes` settings and the `{{commands}}` prompt placeholder are gone (old keys are ignored).

## [1.0.1] - 2026-10-28

### Added
- Update mode toggle: Standard (default: `composer update`, minor and patch versions only) or Disaster (security fixes only, the previous behaviour). `update_mode` in the config; switch it on the desktop project screen or with `m` in the TUI.
- Successful runs end with a Markdown table of the production packages that changed in `composer.lock`, like `composer-lock-diff --no-links --only-prod --md`.

## [1.0.0] - 2026-10-27

First versioned release.

### Added
- Batch updates of local Drupal/DDEV projects from a terminal UI (`drupal-updater`) and a desktop app (Wails, macOS DMG and Linux tarballs).
- Claude mode (default): each project is updated by an autonomous Claude Code session in a private tmux server, on a temp branch you rename or clean up afterwards.
- Updates Drupal core, Symfony and Twig within their constraints, plus every security advisory from `composer audit` that needs no major upgrade (core may move to a newer minor, never a new major).
- Smoke tests before and after the update (bootstrap, common anonymous and admin pages, error logs, pending config); regressions are fixed, rolled back, or fail the run. Extra paths per project via `smoke_paths`, extra flows via the project's `AGENTS.md`.
- Live view of each Claude session in the TUI and desktop app, without attaching to tmux.
- Hard limits for the agent via git/ddev guard shims: no commits, merges or pushes, nothing that destroys local work or the database.
- Update sessions run as vanilla Claude Code (no user plugins, hooks, skills or CLAUDE.md), about 70% fewer input tokens per turn.
- Classic mode: the configured shell commands with retries and auto-healing, no Claude.
- Build scripts for macOS (`scripts/build-dmg.sh`) and Linux amd64/arm64 (`scripts/build-linux.sh`, `scripts/build-linux-docker.sh`), and a CI pipeline that builds them for each release tag.

[Unreleased]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.5...1.0.x
[1.0.6]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.5...v1.0.6
[1.0.5]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.4...v1.0.5
[1.0.4]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.2...v1.0.4
[1.0.2]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.1...v1.0.2
[1.0.1]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/RandalVanheede/drupal-updater/releases/tag/v1.0.0
