# Drupal Updater

Batch-updates local Drupal projects. Two frontends share one Go engine: a terminal UI ([Bubbletea](https://github.com/charmbracelet/bubbletea)) and a desktop app ([Wails](https://wails.io) + Svelte). Runs on macOS and Linux.

## Architecture

```
drupal-updater/
├── main.go          # TUI entrypoint (`--config` opens the config editor, `api` is a hidden JSON CLI)
├── api/             # hidden `api` subcommand: JSON over stdin/stdout for scripting (the desktop app calls Go bindings directly)
├── coreupdates/     # Drupal core release history (updates.drupal.org) and installed-vs-latest status
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

## Releases

Semantic versioning; changes go in `CHANGELOG.md` (Keep a Changelog). Work happens on release branches named `<major>.<minor>.x` (currently `1.2.x`; a new minor version starts a new branch). To release: move the `[Unreleased]` entries into a new version section, commit, then `git tag vX.Y.Z && git push origin <branch> vX.Y.Z`. CI (`.github/workflows/build.yml`) tests every push and, for a `v*` tag, builds the macOS DMG (arm64) plus macOS TUI binaries and the Linux tarballs (amd64, arm64), and publishes them as a GitHub release with that version's changelog section as notes.

## Configuration

Config file location: `~/.config/drupal-updater/config.yaml`

```yaml
base_folders:                           # each scanned for projects (see scan_depth)
  - ~/projects
  - ~/clients
scan_depth: 1                           # folder levels below a base folder; 2 finds ~/clients/<customer>/<project>
project_paths:                          # single projects outside the base folders
  - ~/work/legacy-site

projects:
  my-drupal-site:
    git_start_branch: main              # default: master|main
    git_new_branch_pattern: "release/{date}-*"  # {date} = YYYY-MM-DD, * = project slug
    ignored_packages:
      - drupal/some-module
```

An old single `base_folder:` key is still read and moved into `base_folders` on the next save.

### Shared project settings (`drupal-updater.yaml`)

Commit a `drupal-updater.yaml` in a project's root so every developer runs it the same way. It takes the same keys as a `projects:` entry (a JSON body works too, YAML reads it):

```yaml
git_start_branch: main
git_new_branch_pattern: "release/{date}-*"
git_merge_target: develop            # merge request target, default: the start branch
git_commit_message: "Drupal updates {date}"
ignored_packages: [drupal/some-module]
smoke_paths: [/contact, "admin:/admin/commerce"]
multisite: on                        # drush deploy/updb/cex and the smoke test per site ddev reaches (--uri);
                                     # off (default): only the site in .drush_subsite, or default
claude_instructions: |
  Never update the search_api_solr config.
```

Per field, the repo file wins; the local `projects:` entry only fills fields the file leaves out. A file that does not parse is logged and ignored. It is read from the working tree when a run starts. In the desktop app, Settings → Project Overrides → "Save to repo" (TUI: `w` on a project) writes a project's local overrides into the file for you to commit; settings already in the file that you have not set locally are kept.

### Defaults

`defaults:` in config.yaml (desktop: Settings → Branch defaults) applies to every project:

```yaml
defaults:
  git_start_branch: develop
  git_new_branch_pattern: "release/{date}-*"
  git_merge_target: staging
  git_commit_message: "Drupal updates {date}"
```

Per field the order is: the repo's `drupal-updater.yaml`, the project's `projects:` entry, `defaults:`, then these built-in defaults:

| Setting                  | Default                         |
|--------------------------|---------------------------------|
| `git_start_branch`       | `master\|main` (first that exists) |
| `git_new_branch_pattern` | `hotfix/{date}-drupal-updates`  |

The final branch name is pre-filled from the pattern and can be edited before the rename. If that branch already exists (a second run on the same day) `-2`, `-3`, ... is appended. The old default `hotfix/*` is treated as the new default.

## Merge requests (desktop)

After confirming, each renamed branch gets a "Create MR" button ("Create all MRs" does every one). It:

1. commits whatever the run left uncommitted (`git add -A`, message from `git_commit_message`),
2. fetches `origin/<git_merge_target>` (default: the start branch) and dry-runs the merge,
3. on a conflict, starts the merge without committing and hands it to a Claude session (same tmux setup as a run; it may run composer/drush but still cannot commit or push), then commits the merge; if Claude fails, `git merge --abort` puts the branch back,
4. pushes the branch and runs `glab mr create` (GitLab) or `gh pr create` (an `origin` on github.com) with the run summary as description, then shows an "Open MR" link.

Needs [glab](https://gitlab.com/gitlab-org/cli) logged in for every GitLab host (`glab auth login`, and `glab auth login --hostname gitlab.example.com` for self-hosted); glab picks the host from the project's `origin` remote. Projects on github.com need [gh](https://cli.github.com) logged in instead (`gh auth login`); the pull request always goes to the `origin` repository. Git 2.38+ is required for the dry run.

## Auto-update

Both apps check for updates; the TUI shows "vX available" under the project list and installs with `U` (macOS: replaces its own binary, so it must be writable; Linux: runs the tarball's `install.sh`), then restarts itself. The desktop app checks `releases/latest` of this repo on start and daily, and shows a banner when a newer tag exists. "Update & restart" downloads the asset for the platform (macOS arm64 DMG, Linux x86_64/aarch64 tarball), verifies `<asset>.sig` with `updater.PublicKey`, then swaps the `.app` bundle (macOS) or runs `install.sh` (Linux) and restarts. Intel Macs and builds without `updater.PublicKey` only get a link to the release.

One-time setup: `go run ./scripts/sign -genkey`, store the private key as the GitHub secret `UPDATE_SIGNING_KEY` and put the public key in `updater.PublicKey`. The release job signs every DMG and tarball with it. Builds get their version from `VERSION` (`VERSION=v1.2.3 scripts/build-dmg.sh`; CI passes the tag); without it the app is `dev` and never updates.

## Auto-Detection

On startup the app scans every base folder for subdirectories (down to `scan_depth` levels) whose `composer.json` requires `drupal/core*` (core, recommended, dev, …) or `drupal/cms` and is not itself a Drupal module, theme or profile (`type: drupal-*`). Other projects and folders with only a `.ddev/` are ignored.

Each match, plus every `project_paths` entry, becomes a selectable project. A project is named after its folder; when two projects share a folder name, both are shown as `<parent>/<folder>` (or the full path if that still clashes). That name is also the key under `projects:`, so settings stored under the plain folder name must be moved when a duplicate appears.

## Desktop flow

- **First launch:** a welcome card asks for theme (system, light, dark), font (Atkinson Hyperlegible, JetBrains Mono, Space Grotesk) and font size, then a short guided tour starts. Replay it with the `?` button in the header or from Settings → Appearance. These preferences live in the webview's local storage (per machine), not in `config.yaml`; the window size is remembered in `~/.config/drupal-updater/window.json`.
- **Project list:** each row shows the installed Drupal core version with a status dot: green = newest release of its major, orange = behind but no security update, red = a security update is available (or the installed release is flagged insecure). The version comes from `composer.lock` on origin's start branch (`git_start_branch`), so a stale local checkout does not show as out of date; without such a branch the working-tree lock file is used. Filter by name and by status (all, up to date, behind, security); Toggle All acts on the rows the filters show.
- **Refreshing:** origin is fetched at most once per 24 hours per project (judged by the age of `.git/FETCH_HEAD`, so a fetch you ran yourself counts), 4 projects at a time, never for a project that is updating, and a failed fetch (offline, no key) is not retried until the app restarts. The **Refresh** button fetches everything now and downloads Drupal's release list again. The list is cached for an hour; offline, the dots are simply left out and a note says so.
- **Mode:** the Standard/Disaster switch below the list chooses all minor and patch updates or security fixes only.
- **Running:** a finished project can be confirmed, retried, turned into a merge request and dismissed right away. A run that fails or times out keeps any work it committed on a `drupal-updater/failed-<project>-<time>` branch. Closing the window while updates run asks first.
- **Dashboard (optional):** Settings → Dashboard connects to Dynamate's applications dashboard (`dashboard_url`, default `https://applications.dyna-ops.be/api/mcp`) with a read-only browser sign-in. Matched projects get an SLA tag with a detail card and a link; filter by SLA. Projects are matched by name, then detected from the git remote's group, the DDEV project name and the Drupal site name; confirm or fix a match in the settings tab, which saves `dashboard_project: <name>` in the project's `drupal-updater.yaml`. The dashboard's module data comes from connectors on each site and can be days old (the card says so); the version dots always come from the repository.
- **Settings:** vertical tabs; Save is pinned at the bottom and an "Unsaved changes" note shows when something is edited. An unreadable `config.yaml` is kept as `config.yaml.bad` and the app starts on defaults with a notice.

## TUI Flow

The project list shows each project's installed Drupal core version with a marker (green ● newest release, yellow ■ behind, red ◆ security update available), read the same way as in the desktop app (origin's start branch, fetched at most daily). `r` fetches everything now and re-checks Drupal's releases.

1. **List View** — Scrollable list of detected projects with checkboxes. Toggle individual projects with `space`/`enter`, toggle all with `a`, start update with `u`, switch update mode with `m`, open settings with `S`, install an available update with `U`.
2. **Running View** — Shows live status per project (`IDLE`, `RUNNING`, `SUCCESS`, `FAILED`) and the live Claude session.
3. **Confirm View** — Rename each successful temp branch to its final name, or clean up.

## Claude runner

A run has three parts.

1. **The app prepares**, in Go: checks out the start branch, fetches origin and fast-forwards (local changes are set aside and put back), creates the temp branch, starts ddev (unless it already runs), `ddev composer install` and `ddev drush deploy -y`. It then lists what there is to do: `composer audit` in disaster mode, `composer outdated --minor-only` in standard mode. **Nothing to do means the project is skipped**: no Claude session, back on the start branch, status "Skipped". Otherwise it takes a baseline smoke test of the site.
2. **Claude updates**, in a detached tmux session (`du-<project>`, `claude --model sonnet`), with that list in its prompt. A fixed step that failed is handed over too, with its output, so Claude finishes it first.
3. **The app checks**, in Go: `ddev drush updb -y` and `cex -y`, no pending database updates or config changes, the same smoke test again, and new Error-level log entries. Problems go back to Claude in a repair session (at most two); if they remain, the run fails. An update that did not change `composer.lock` is reported as skipped. Finally ddev is stopped, unless it was running before.

A failed or cancelled run returns the project to its start branch; committed work is kept on a `drupal-updater/failed-*` branch and uncommitted work is stashed. The failure is saved and given to the next attempt; the summary of a successful run is saved for merge requests after a restart. A session that stops on a usage or rate limit fails at once, one that sits idle without a result for ten minutes fails too. At most one project per 2.5 GiB of Docker memory runs at the same time, whatever `max_concurrent` says. Attach with `tmux -L drupal-updater attach -t du-<project>` (private tmux socket; tmux is bundled in the .app by `scripts/bundle-tmux.sh`).

```yaml
update_mode: standard           # or "disaster"; toggle on the project screen (desktop) or with `m` (TUI)
claude_model: sonnet            # always passed via --model
claude_prompt: "..."            # template; default is executor.DefaultClaudePrompt. When a new version changes
                                # the built-in prompt, the desktop app shows both and lets you merge them
                                # placeholders: {{project}} {{path}} {{start_branch}} {{temp_branch}} {{setup}} {{update_list}} {{sites}} {{previous_failure}} {{ignored_packages}} {{project_instructions}} {{updates}} {{result_file}}
claude_allowed_tools: []        # default: all tools (Read Grep Glob Edit Write Bash WebFetch WebSearch)
claude_permission_mode: dontAsk # unlisted tools are denied, never prompt
claude_timeout_minutes: 60
```

`update_mode: disaster` fixes every security advisory `composer audit` reports (Drupal core, contrib and other packages) that needs no major upgrade, and nothing else: core may move to a newer minor (10.3 → 10.5) when an advisory needs it, never to a new major. Skipped advisories are listed with their reason in the result summary, and the app adds the advisories still open.
The default, `update_mode: standard`, updates core, Symfony and Twig, then runs a plain `composer update --with-all-dependencies` within the existing constraints and pins back any package that jumped a major (loose constraints such as `*`), so only minor and patch updates land.

On success the result also lists the production packages that changed in `composer.lock`, as a Markdown table like `composer-lock-diff --no-links --only-prod --md` (built in, no PHP needed on the host).

The smoke test is run by the app, before and after the update, with the same checks: Drupal bootstraps and `drush cr` works, anonymous pages (`/`, `/user/login`, the newest node of each content type, a 404) and admin pages (content, structure, config, node add forms, status report, through a `drush uli` login) answer without errors, and no new Error-level log entries appear. It talks to the site over ddev's local http URL. Only checks that passed before and fail after count as regressions. Add project-specific paths per project:

```yaml
projects:
  my-site:
    smoke_paths: [/nl/contact, "admin:/admin/structure/webform"]   # admin: = checked while logged in
```

Flows that need more than loading a page belong in the project's own repo, in `AGENTS.md` (the open standard for coding-agent instructions; `CLAUDE.md` is read when there is no `AGENTS.md`). The agent reads it before starting and follows it while updating and repairing, e.g.:

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
