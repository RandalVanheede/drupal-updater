# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Release branches are named `<major>.<minor>.x`.

## [Unreleased]

## [1.5.2] - 2026-11-25
### Changed
- Refresh updates each project's core version and status dot as soon as that project is done, instead of all at once at the end.

## [1.5.1] - 2026-11-23
### Added
- Multisite is also a general setting (Settings → Projects, TUI Projects section), for every project unless a project overrides it.

### Changed
- A project's Multisite switch shows the general setting until it is changed there; a project stores its own value only when it differs.

## [1.5.0] - 2026-11-23
### Added
- Multisite per project (`multisite: on`, project settings): `drush deploy`, `updb`, `cex`, the pending checks and the smoke test run once per site ddev reaches, with `--uri`. Sites without a ddev hostname or that do not bootstrap are skipped and reported as pre-existing. Off (default): drush runs for the site in `.drush_subsite`, or the default site.
- `{{sites}}` prompt placeholder: the sites the app runs for, with their URLs.
- The built-in prompt makes sure `drupal/core-vendor-hardening` is installed, allowed and configured with the core files to remove; a project where it is missing is no longer skipped as up to date.
- Desktop: when the built-in prompt changes, a custom `claude_prompt` is merged in a dialog: per difference yours, the new text or both, then an editable final version. A prompt set aside by 1.4 (`<config>.claude_prompt.bak`) is offered the same way once.

## [1.4.1] - 2026-11-18
### Added
- Projects on github.com get a pull request through `gh` (`gh auth login`) instead of a GitLab merge request through `glab`; the pull request always goes to the `origin` repository the branch was pushed to.
- Settings: "Show placeholders" opens an explanation of every prompt placeholder; "How the smoke test works" (project overrides) explains the checks and how to add pages and flows.

### Changed
- Claude's summary has one line per package group, core first with the advisories it fixes, and no "none" lines.
- The result card can be folded; after a run the last Claude screen is folded like the full log.
- A failed merge request message can be clicked to show it in full; when glab does not know the origin's GitLab server, the message says which `glab auth login --hostname` to run.

### Security
- Frontend build dependencies updated (`npm audit fix`: devalue, nanoid).

## [1.4.0] - 2026-11-18
### Changed
- Updates run in three parts. The app does the fixed steps itself (checkout, pull, temp branch, `ddev start`, `composer install`, `drush deploy`), lists what there is to update (`composer audit` in disaster mode, `composer outdated --minor-only` in standard mode) and gives that list to Claude. A step that fails is handed to Claude with its output instead of failing the run. Afterwards the app runs `drush updb` and `cex`, checks that no database updates or config changes are pending, smoke-tests the site against a baseline it took before the update and looks for new errors in the Drupal log; problems go to a repair session (at most two) and fail the run if they remain.
- Projects with nothing to update are skipped without starting Claude (status "Skipped"); so is an update that left `composer.lock` unchanged.
- Disaster mode is security-only: the core, Symfony and Twig update now belongs to standard mode.
- The smoke test is run by the app over ddev's local http URL, including admin pages through a `drush uli` login; flows from a project's `AGENTS.md` are carried out by Claude after updating.
- Desktop: the project list is grouped by urgency (security update, update available, up to date, not checked) with Select all per group and the version each project moves to; the folder path and the dashboard details are in a detail card behind the `i` button; "Has SLA" replaces the SLA selector; the button states how many projects will run. Settings without card borders, one type scale, sentence-case labels; the tour covers the new list and the dashboard.
- At most one project per 2.5 GiB of Docker memory runs at a time, whatever `max_concurrent` says. ddev is only stopped afterwards when it was not running before.
- A custom `claude_prompt` written for the old flow (without `{{update_list}}`) is dropped in favour of the built-in prompt; the old one is saved next to the config as `<config>.claude_prompt.bak`.
- Readable results: a finished run opens with a card saying why it failed or what was done, in sections (update session, each repair session, what the app checked, the `composer.lock` changes as a table), with the full log folded below. Claude writes its summary as one fact per line.
- The start branch is brought up to date with the same safe fetch as the core status (the project's own git config is not read) and a fast-forward-only merge, instead of `git pull`.

### Fixed
- Cancelling a run returns the project to its start branch like a failed run, keeping or stashing the work.
- A session that stops on a usage or rate limit fails at once instead of after the full timeout; one that stays idle without a result for ten minutes fails too.
- An update was failed after two pointless repair sessions when `drush updatedb:status` answered "No database updates required" (or `config:status` "No differences"): those messages were taken for pending updates.
- In disaster mode, packages listed as not to be updated were still handed to Claude as advisories; an audit result that cannot be read no longer counts as "no advisories".
- An earlier run's temp branch with commits is kept (renamed) instead of reset at the next start; git commands are no longer killed halfway when a run is cancelled; command output is capped at 1 MB; the smoke test only follows redirects within the site.
- The summary of a successful run is kept on disk, so a merge request made after a restart gets the real description; the next attempt after a failure is told what went wrong.

## [1.3.1] - 2026-11-17
### Security
- The automatic `git fetch` no longer reads the project's `.git/config` (writable from the ddev containers), which could make git run programs or steer the network (remote helpers, `remote.origin.vcs`, `core.gitProxy`, `uploadpack`, `core.alternateRefsCommand`, credential helpers, cookie files, proxies). It now runs in a scratch repository that only has your own system and global git configuration (so SSH settings and the macOS Keychain keep working), writes the objects into the project and copies the branch positions over with `update-ref`. Only https, ssh and scp-style origin URLs are fetched (including host aliases from `~/.ssh/config`); shallow clones keep working. Per-folder `includeIf "gitdir:…"` blocks in your git config no longer apply to this fetch.
- Every git command the app runs now sets `GIT_NO_LAZY_FETCH=1`: a partial-clone "promisor" remote written into a project's `.git/config` could otherwise run a program whenever git read a missing object, including during the core-version check.
- Signing out of the dashboard only sends a stored refresh token to the revocation endpoint of the server it was issued for.

## [1.3.0] - 2026-11-17
### Added
- Desktop app: optional connection to the applications dashboard (MCP server). Sign in once through the browser (OAuth with PKCE, read-only); each matched project then shows its SLA with a detail card on hover (customer, account manager, PHP, last scan, end-of-life runtimes, every pending security module) and a link to its dashboard page. Filter the list by SLA. The address defaults to the Dynamate dashboard (`dashboard_url`); tokens live in `~/.config/drupal-updater/dashboard.json` (0600).
- Local projects are matched to dashboard projects by name, and otherwise detected from the git remote's group, the DDEV project name and the Drupal site name. Detected matches are listed in Settings → Dashboard with the reason and can be saved; unmatched projects get suggestions and a picker. A match is saved as `dashboard_project` in the project's `drupal-updater.yaml` (or a local project override).
- Desktop and TUI: each project shows its installed Drupal core version with a status marker (green newest release, orange behind, red security update), read from `composer.lock` on origin's start branch. Origin is fetched at most once per 24 hours per project; a Refresh button (desktop) or `r` (TUI) fetches now. Desktop: filter by update status.
- Desktop: Settings redesign with vertical tabs, a Save bar that stays put and no longer leaves the page, "Unsaved changes" marker, folder pickers with removable entries, sliders for scan depth, concurrency (coloured by load) and timeout, ordered start-branch list, exclusion list, a live count of the projects the settings find, a Standard/Disaster switch, and Appearance (theme, font, font size). First launch asks for the appearance and runs a short tour (replay with `?`). The window size is remembered and the macOS title bar is integrated.

### Fixed
- A run that fails or times out keeps the commits it made on a `drupal-updater/failed-<project>-<time>` branch instead of deleting them.
- An unreadable `config.yaml` is kept as `config.yaml.bad-<time>` instead of being overwritten by defaults on the next save; config and window-size files are written atomically.
- Cancel All also cancels running merge requests; closing the window while updates run asks first.
- The tour's Enter key advanced twice; several error states in the desktop app were silent.

### Security
- The automatic `git fetch` only honours an ssh command from the user's own git config, never one written into the repository's `.git/config`.
- The dashboard's project links are only opened when they are https on the dashboard's own host; sign-in ignores callbacks that do not carry its state, and token requests never follow redirects to another host.

## [1.2.1] - 2026-11-17
### Changed
- Desktop app: a project's Drupal core version (and its status dot) is read from `composer.lock` on origin's start branch instead of the working tree, so a stale local checkout no longer shows as out of date. Origin is fetched at most once per 24 hours per project (by the age of git's `FETCH_HEAD`); a failed fetch is not retried until the app restarts.
- Desktop app: a Refresh button next to "Toggle All" fetches every project now and downloads Drupal's release list again.

## [1.2.0] - 2026-11-17
### Added
- Desktop app: each project shows its installed Drupal core version (from `composer.lock`) with a status dot: green is the newest release, orange is behind without a security update, red needs a security update. The status comes from Drupal's release history (updates.drupal.org, cached for an hour; offline simply shows no dots).
- Desktop app: filter the project list by update status (all, up to date, behind, security), next to the text filter. "Toggle All" now only toggles the projects the filters show.
- Desktop app: Standard/Disaster mode is a toggle switch.

### Changed
- Desktop app: Settings uses vertical tabs (Appearance last), card sections and segmented controls, with the Save bar pinned to the bottom. The tour opens the advanced Claude settings.
- Desktop app: project rows use fixed columns, and a long path is cut at the front instead of wrapping.

## [1.1.0] - 2026-11-17
### Added
- Desktop app: Settings → Appearance with theme (follow system, light, dark), font (Atkinson Hyperlegible, JetBrains Mono, Space Grotesk) and font size. They apply immediately and are stored per machine.
- Desktop app: a first-run welcome that asks for these appearance preferences, followed by a short guided tour. Replay the tour with the `?` button in the header or from Settings.
- Desktop app: the window size is remembered between launches.

### Changed
- Desktop app: on macOS the window uses an integrated title bar, and the logo follows the theme colour.

## [1.0.11] - 2026-11-17
### Changed
- Desktop app: a finished project can be confirmed (branch rename or cleanup), retried, turned into a merge request and dismissed right away, without waiting for the other projects. Starting more projects no longer drops finished rows that are still waiting for confirmation. The separate confirm screen is gone; each row has its own buttons, plus "Confirm all finished" for the bulk case.

## [1.0.10] - 2026-11-17
### Changed
- `git_start_branch` accepts a `|`-separated list; the first branch that exists locally wins. The default is now `master|main`. Merge requests are refused from any listed branch.

## [1.0.9] - 2026-11-17
### Fixed
- Claude no longer stops on the "Adding a new directory" trust prompt for the run's `out` folder (seen on Linux); its exact path is pre-trusted (never a surrounding git repo). 1.0.8 was withdrawn before publishing.

## [1.0.7] - 2026-11-17
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

[Unreleased]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.10...1.0.x
[1.0.10]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.9...v1.0.10
[1.0.9]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.7...v1.0.9
[1.0.7]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.6...v1.0.7
[1.0.6]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.5...v1.0.6
[1.0.5]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.4...v1.0.5
[1.0.4]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.2...v1.0.4
[1.0.2]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.1...v1.0.2
[1.0.1]: https://github.com/RandalVanheede/drupal-updater/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/RandalVanheede/drupal-updater/releases/tag/v1.0.0
