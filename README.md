# Drupal Updater

Batch-updates local Drupal projects. Two frontends share one Go engine: a terminal UI ([Bubbletea](https://github.com/charmbracelet/bubbletea)) and a desktop app ([Wails](https://wails.io) + Svelte). Runs on macOS and Linu## Architecture

```
drupal-updater/
├── main.go          # TUI entrypoint (`--config` opens the config editor, `api` is a hidden JSON CLI)
├── api/             # JSON/stdin/stdout commands used by the desktop app
├── config/          # YAML config loading & per-project defaults
├── discovery/       # Auto-detects Drupal/DDEV projects in the base folders
├── executor/        # Runs updates per project (Claude in tmux)
├── logging/         # File logger
├── ui/              # Bubbletea TUI (list, input, running views, config editor)
├── desktop/         # Wails desktop app (Go bindings in main.go, Svelte frontend)
└── scripts/         # build-dmg.sh (macOS), build-linux.sh, build-linux-docker.sh (any CPU), bundle-tmux.sh
```

## Building

| Target | Command | Output |
|--------|---------|--------|
| TUI (any OS) | `go build -o drupal-updater .` | `./drupal-updater` |
| macOS desktop | `scripts/build-dmg.sh` | `desktop/build/bin/Drupal-Updater.dmg` (tmux bundled; the `.app` is not kept, so Spotlight only finds the installed one) |
| Linux desktop | `scripts/build-linux.sh` (on Linux, for its own CPU; needs `libgtk-3-dev` + `libwebkit2gtk-4.1-dev` or `-4.0-dev`) | `desktop/build/bin/drupal-updater-linux-<arch>.tar.gz`, unpack and run `install.sh` |
| Linux desktop, any host/CPU | `scripts/build-linux-docker.sh [amd64] [arm64]` (needs Docker; default both) | `drupal-updater-linux-x86_64.tar.gz`, `drupal-updater-linux-aarch64.tar.gz` |

Wails can't cross-compile between operating systems or CPUs (webkit needs cgo), so `build-linux-docker.sh` builds inside a Linux container per architecture; a foreign CPU is emulated by Docker and takes several minutes. On Linux, tmux and claude must be installed on the system.

``

## Releases

Semantic versioning; changes go in `CHANGELOG.md` (Keep a Changelog). Work happens on release branches named `<major>.<minor>.x` (currently `1.0.x`). To release: move the `[Unreleased]` entries into a new version section, commit, then `git tag vX.Y.Z && git push origin 1.0.x vX.Y.Z`. CI (`.github/workflows/build.yml`) tests every push and, for a `v*` tag, builds the macOS DMG (arm64) plus macOS TUI binaries and the Linux tarballs (amd64, arm64), and publishes them as a GitHub release with that version's changelog section as notes.

## Configuration

Config file location: `~/.config/drupal-updater/config.yaml`

```yaml
base_folders:                           # each scanned one level deep for projects
  - ~/projects
  - ~/clients
project_paths:                          # single projects outside the base folders
  - ~/work/legacy-site

projects:
  my-drupal-site:
    git_start_branch: main              # default: master
    git_new_branch_pattern: "hotfix/*"  # * = project slug
    ignored_packages:
      - drupal/some-module
```

An old single `base_folder:` key is still read and moved into `base_folders` on the next save.

### Defaults

If a project has no config entry, these defaults apply:

| Setting                  | Default     |
|--------------------------|-------------|
| `git_start_branch`       | `master`    |
| `git_new_branch_pattern` | `hotfix/*`  |

## Auto-Detection

On startup the app scans every base folder for subdirectories containing either:
- A `.ddev/` directory, **or**
- A `composer.json` file

Each match, plus every `project_paths` entry, becomes a selectable project. A project is named after its folder; when two projects share a folder name, both are shown as `<parent>/<folder>` (or the full path if that still clashes). That name is also the key under `projects:`, so settings stored under the plain folder name must be moved when a duplicate appears.

## TUI Flow

1. **List View** — Scrollable list of detected projects with checkboxes. Toggle individual projects with `space`/`enter`, toggle all with `a`, start update with `u`.
2. **Running View** — Shows live status per project (`IDLE`, `RUNNING`, `SUCCESS`, `FAILED`) and the live Claude session.
3. **Confirm View** — Rename each successful temp branch to its final name, or clean up.

## Claude runner

Each project's update runs in a detached tmux session (`du-<project>`) with `claude --model sonnet`.
The app only starts the session, waits for `~/.config/drupal-updater/runs/<project>/out/result.json`, verifies the
repo is on the temp branch, and cleans up on failure. Attach with `tmux -L drupal-updater attach -t du-<project>` (private tmux socket; tmux is bundled in the .app by `scripts/bundle-tmux.sh`).

```yaml
update_mode: standard           # or "disaster"; toggle on the project screen (desktop) or with `m` (TUI)
claude_model: sonnet            # always passed via --model
claude_prompt: "..."            # template; default is executor.DefaultClaudePrompt
                                # placeholders: {{project}} {{path}} {{start_branch}} {{temp_branch}} {{ignored_packages}} {{smoke_paths}} {{project_instructions}} {{updates}} {{result_file}}
claude_allowed_tools: []        # default: all tools (Read Grep Glob Edit Write Bash WebFetch WebSearch)
claude_permission_mode: dontAsk # unlisted tools are denied, never prompt
claude_timeout_minutes: 60
```

The default prompt updates core/symfony/twig within their constraints, then fixes every security advisory `composer audit` reports (Drupal core, contrib and other packages) that needs no major upgrade: core may move to a newer minor (10.3 → 10.5) but never a new major, other packages stay on their installed major. Skipped advisories are listed with their reason in the result summary.
That is `update_mode: disaster`. The default, `update_mode: standard`, runs a plain `composer update --with-all-dependencies` within the existing constraints and pins back any package that jumped a major (loose constraints such as `*`), so only minor and patch updates land.

On success the result also lists the production packages that changed in `composer.lock`, as a Markdown table like `composer-lock-diff --no-links --only-prod --md` (built in, no PHP needed on the host).

Around the update the agent smoke-tests the site: once before updating (baseline) and once after `cex`, with the same checks: Drupal bootstraps, anonymous pages (`/`, `/user/login`, newest node per content type, search, a 404) and admin pages (content, structure, config, node add forms, status report) return 200 without PHP errors, no new Error-level log entries, no pending config. Only checks that passed before and fail after count as regressions; the agent fixes them or rolls back the causing package, and fails the run if one remains. Add project-specific paths per project:

```yaml
projects:
  my-site:
    smoke_paths: [/nl/contact, "admin:/admin/structure/webform"]   # admin: = checked while logged in
```

Flows that need more than loading a page belong in the project's own repo, in `AGENTS.md` (the open standard for coding-agent instructions; `CLAUDE.md` is read when there is no `AGENTS.md`). The agent reads it before starting and runs its testing section at both smoke-test steps, e.g.:

```markdown
## Smoke tests
- Submit the contact form at /nl/contact; the confirmation message appears and a mail shows up in Mailpit.
- Log in as an editor, create a news item with an image, check it on /nl/nieuws.
```

Update sessions run vanilla Claude Code: no CLAUDE.md files, no user/project settings (so no plugins or hooks of your own), no skills, core tools only. That cut input tokens by ~70% in a test run.

The agent works autonomously with every tool. Hard limits, enforced twice (permission deny rules, and read-only `git`/`ddev` shims in `runs/<project>/bin` that run the app's hidden `guard` subcommand, `executor/guard.go`, which also catches aliases, global options and grouped flags):
- **No commits, merges or pushes**: `git commit/merge/push/cherry-pick/revert/am`, and `git pull` without `--rebase`/`--ff-only`.
- **No destroying local work or data**: `git reset --hard`, `git clean`, `git stash drop/clear`, `ddev delete/clean`, `--reset-database`, `-R/--remove-data`, `snapshot -C/--cleanup`.

These stop mistakes, not a determined agent (git called by full path skips the shim). MCP servers and the repo's own `.claude/` settings are disabled in these sessions.
A project folder must be trusted by Claude once (run `claude` in it); otherwise the run fails with that instruction.
Integration tests (real Claude): `DU_E2E_DIR=<trusted dir> go test -tags integration ./executor`.
