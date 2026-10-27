# Drupal Updater

Batch-updates local Drupal projects. Two frontends share one Go engine: a terminal UI ([Bubbletea](https://github.com/charmbracelet/bubbletea)) and a desktop app ([Wails](https://wails.io) + Svelte). Runs on macOS and Linu## Architecture

```
drupal-updater/
├── main.go          # TUI entrypoint (`--config` opens the config editor, `api` is a hidden JSON CLI)
├── api/             # JSON/stdin/stdout commands used by the desktop app
├── config/          # YAML config loading & per-project defaults
├── discovery/       # Auto-detects Drupal/DDEV projects in base folder
├── executor/        # Runs updates per project (classic shell mode or Claude in tmux)
├── logging/         # File logger
├── ui/              # Bubbletea TUI (list, input, running views, config editor)
├── desktop/         # Wails desktop app (Go bindings in main.go, Svelte frontend)
└── scripts/         # build-dmg.sh (macOS), build-linux.sh, bundle-tmux.sh
```

## Building

| Target | Command | Output |
|--------|---------|--------|
| TUI (any OS) | `go build -o drupal-updater .` | `./drupal-updater` |
| macOS desktop | `scripts/build-dmg.sh` | `desktop/build/bin/Drupal-Updater.dmg` (tmux bundled; the `.app` is not kept, so Spotlight only finds the installed one) |
| Linux desktop | `scripts/build-linux.sh` (on Linux; needs `libgtk-3-dev` + `libwebkit2gtk-4.1-dev` or `-4.0-dev`) | `desktop/build/bin/drupal-updater-linux-<arch>.tar.gz`, unpack and run `install.sh` |

Wails can't cross-compile between operating systems, so build the Linux desktop app on Linux (or in Docker). On Linux, tmux and claude must be installed on the system.

``

## Configuration

Config file location: `~/.config/drupal-updater/config.yaml`

```yaml
base_folder: ~/projects

projects:
  my-drupal-site:
    git_start_branch: main              # default: master
    git_new_branch_pattern: "hotfix/*"  # * is replaced with user input
    ignored_packages:
      - drupal/some-module
    commands:                           # override the default command flow
      - git pull --rebase --autostash
      - ddev start
      - ddev composer install
      - ddev drush deploy -y
      - ddev composer update 'drupal/core-*' --with-all-dependencies
      - ddev drush updb -y
      - ddev drush cex -y
      - ddev stop
```

### Defaults

If a project has no config entry, these defaults apply:

| Setting                | Default                                                                                     |
|------------------------|---------------------------------------------------------------------------------------------|
| `git_start_branch`     | `master`                                                                                    |
| `git_new_branch_pattern` | `hotfix/*`                                                                                |
| `commands`             | `git pull`, `ddev start`, `ddev composer install`, `ddev drush deploy -y`, `ddev composer update 'drupal/core-*' 'symfony/*' 'twig/*' --with-all-dependencies`, `ddev drush updb -y`, `ddev drush cex -y` |

## Auto-Detection

On startup the app scans `base_folder` for subdirectories containing either:
- A `.ddev/` directory, **or**
- A `composer.json` file

Each match becomes a selectable project in the TUI.

## TUI Flow

1. **List View** — Scrollable list of detected projects with checkboxes. Toggle individual projects with `space`/`enter`, toggle all with `a`, start update with `u`.
2. **Input View** — If any selected project's branch pattern contains `*`, the user is prompted to type a variable (e.g. ticket number). Press `enter` to confirm or `esc` to go back.
3. **Running View** — Shows live status per project:
   - `IDLE` (grey) — not started
   - `RUNNING` (yellow) — commands in progress, shows current command
   - `SUCCESS` (green) — all commands completed
   - `FAILED` (red) — a command exited non-zero, shows error output

## Execution Engine

- Each selected project runs in its own **goroutine**.
- Commands execute sequentially via `sh -c` (supports quotes, globs, pipes).
- Before the user's command list, the engine prepends:
  1. `git checkout <start_branch>`
  2. `git pull --rebase --autostash`
  3. `git checkout -b <new_branch>` (if branch variable was provided)
- On any non-zero exit code the project is marked **FAILED** and remaining commands are aborted.
- stdout/stderr are captured and shown in the TUI for debugging.

## Error Handling

- **Exit code != 0** → failure. The project turns red. The user must fix it manually.
- Remaining commands for that project are **aborted** (not continued).
- Other projects continue independently.

## Claude mode (default)

Each project's update runs in a detached tmux session (`du-<project>`) with `claude --model sonnet`.
The app only starts the session, waits for `~/.config/drupal-updater/runs/<project>/out/result.json` (the only run folder Claude may write to), verifies the
repo is on the temp branch, and cleans up on failure. Attach with `tmux -L drupal-updater attach -t du-<project>` (private tmux socket; tmux is bundled in the .app by `scripts/bundle-tmux.sh`).

```yaml
mode: claude                    # or "classic" (shell commands + auto-heal in Go)
claude_model: sonnet            # always passed via --model
claude_prompt: "..."            # template; default is executor.DefaultClaudePrompt
                                # placeholders: {{project}} {{path}} {{start_branch}} {{temp_branch}} {{commands}} {{result_file}}
claude_allowed_tools: []        # default: Read Grep Glob and an explicit git/ddev subcommand list (see executor/claude.go); Edit/Write are always added, scoped to the project
claude_permission_mode: dontAsk # unlisted tools are denied, never prompt
claude_timeout_minutes: 60
```

Claude's `git` and `ddev` resolve to read-only shims in `runs/<project>/bin` that run the app's hidden `guard` subcommand (`executor/guard.go`):
- **git**: only the subcommands an update needs, no global options, no option (in any abbreviation) that runs programs, writes outside the repo or reaches other repositories; fetch/pull only from configured remotes.
- **ddev**: refused once the host-executing `.ddev` files (`commands/host/`, `providers/`, `config*.yaml`, `docker-compose*.yaml`, symlinks included) differ from the state at run start and from every remote branch fetched at run start or by the guard's own fetch/pull, e.g. after code in a container rewrote them. Refs are never trusted live.
- **both**: refused outside the project folder, or when `.git/config` changed other than through `branch.*` updates by git itself. The app's own post-run git operations (rename, cleanup, branch check) apply the same config check. Git hooks and fsmonitor are always off for Claude's git and the app's own git calls (`.git` is writable from the containers).

Always denied: `git push`, `git reset --hard`, `git clean`, forced git commands. MCP servers and the repo's own `.claude/` settings (hooks, permissions) are disabled in these sessions.
A project folder must be trusted by Claude once (run `claude` in it); otherwise the run fails with that instruction.
Integration tests (real Claude): `DU_E2E_DIR=<trusted dir> go test -tags integration ./executor`.
