# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Release branches are named `<major>.<minor>.x`.

## [Unreleased]

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

[Unreleased]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.2...1.0.x
[1.0.2]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.1...v1.0.2
[1.0.1]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/RandalVanheede/drupal-updater/releases/tag/v1.0.0
