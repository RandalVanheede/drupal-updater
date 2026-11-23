<script>
  import { onMount, tick } from 'svelte';
  import { DashboardCandidates, DashboardState, DashboardSignIn, DashboardSignOut, DashboardInfo, PickFolder, PreviewProjects, GetConfigError, DiscoverProjects, CoreStatuses, RefreshCore, RefreshReleases, GetConfig, SaveConfig, StartUpdate, CancelProject, CancelAll, RenameBranch, CleanupBranch, RetryProjects, GetClaudeDefaults, SetUpdateMode, FinalBranch, SaveProjectFile, MergeRequest, GetVersion, GetUpdate, InstallUpdate, GetPromptMerge, ResolvePromptMerge } from '../wailsjs/go/main/App';
  import { EventsOn, BrowserOpenURL } from '../wailsjs/runtime/runtime';
  import Seg from './Seg.svelte';
  import Welcome from './Welcome.svelte';
  import Tour from './Tour.svelte';
  import ListEditor from './ListEditor.svelte';
  import Slider from './Slider.svelte';
  import DashPopover from './DashPopover.svelte';
  import RunResult from './RunResult.svelte';
  import Explain from './Explain.svelte';
  import PromptMerge from './PromptMerge.svelte';
  import { FONTS, THEMES, SIZES, FONT_ITEMS, load } from './appearance.js';
  import { segKeys } from './keys.js';

  const isMac = navigator.userAgent.includes('Macintosh');

  let theme = $state(load('theme', v => ['system', 'light', 'dark'].includes(v), 'system'));
  let fontSize = $state(load('fontSize', v => ['14', '16', '18', '20'].includes(v), '16'));
  let font = $state(load('font', v => Object.hasOwn(FONTS, v), 'atkinson'));
  const dark = window.matchMedia('(prefers-color-scheme: dark)');
  let systemDark = $state(dark.matches);
  dark.addEventListener('change', e => systemDark = e.matches);
  $effect(() => {
    const root = document.documentElement;
    root.dataset.theme = theme === 'system' ? (systemDark ? 'dark' : 'light') : theme;
    // per-font scale evens out the fonts' different x-heights
    root.style.fontSize = fontSize * FONTS[font].scale + 'px';
    const stack = FONTS[font].css + ', system-ui, sans-serif';
    root.style.setProperty('--font-head', stack);
    root.style.setProperty('--font-body', stack);
    try { localStorage.setItem('theme', theme); localStorage.setItem('fontSize', fontSize); localStorage.setItem('font', font); } catch {}
  });

  // "master|main" <-> ['master', 'main'] (git_start_branch is one "|"-separated string)
  const branches = (v) => (v || '').split('|').map(b => b.trim()).filter(Boolean);
  const loadHint = (v) => v <= 2 ? 'ok' : v <= 4 ? 'mid' : v <= 6 ? 'high' : 'max';
  const loadText = (v) => v === 1 ? 'Gentle: one project at a time, the lowest CPU, memory and token use.'
    : v <= 2 ? 'Light: easy on the machine and on your Claude rate limit.'
    : v <= 4 ? 'Balanced: several Claude sessions at once, noticeable CPU and memory use.'
    : v <= 6 ? 'Heavy: many parallel sessions burn tokens fast and load the machine.'
    : 'Very heavy: likely to hit rate limits and slow the machine down.';
  const depthText = (v) => v === 1 ? 'A project is a direct subfolder of a base folder.'
    : v === 2 ? 'Also looks one folder deeper, e.g. <base>/<customer>/<project>.'
    : 'Deeper levels take longer to scan.';
  // live count of what the (unsaved) project settings would find
  let previewCount = $state(null);
  $effect(() => {
    const args = [$state.snapshot(config.BaseFolders) || [], $state.snapshot(config.ProjectPaths) || [], $state.snapshot(config.ExcludedProjects) || [], Number(config.ScanDepth) || 1];
    const timer = setTimeout(async () => { try { previewCount = await PreviewProjects(...args); } catch { previewCount = null; } }, 300);
    return () => clearTimeout(timer);
  });
  function setMode(m) { config.UpdateMode = m; SetUpdateMode(m); }

  // First run: appearance picker first (changes apply live behind it), then the tour (replayable from the ? button).
  let tour; // the <Tour> component
  let tourActive = $state(false);
  let welcome = $state(false);
  function goTo(v, tab) { view = v; if (tab) settingsTab = tab; }
  function finishWelcome() {
    welcome = false;
    try { localStorage.setItem('setupDone', '1'); } catch {}
    let toured = false;
    try { toured = !!localStorage.getItem('tourDone'); } catch {}
    if (!toured) setTimeout(() => tour.start(), 300);
  }
  onMount(() => {
    let setup = false, toured = false;
    try { setup = !!localStorage.getItem('setupDone'); toured = !!localStorage.getItem('tourDone'); } catch {}
    if (!setup && !toured) welcome = true; // existing users (tour already seen) are not asked
    else if (!toured) setTimeout(() => tour.start(), 600);
  });

  const TABS = [['projects', 'Projects'], ['branches', 'Branch defaults'], ['claude', 'Claude'], ['overrides', 'Project overrides'], ['dashboard', 'Dashboard'], ['appearance', 'Appearance']];
  let settingsTab = $state('projects');

  let view = $state('projects');
  let projects = $state([]);
  let selected = $state({});
  let claudeDefaults = $state({ prompt: '', allowedTools: [] });
  let config = $state({ BaseFolders: [], ProjectPaths: [], MaxConcurrent: 3, ExcludedProjects: [], Projects: {} });

  let statuses = $state({});
  let logs = $state({});
  let results = $state({});
  let mergeOpen = $state({}); // failed merge request messages shown in full // the final report of each run, shown above the log
  let screens = $state({}); // live Claude terminal per project
  let notStarted = $state({}); // projects that failed before running (discovery), so confirm must not clean up their branch
  let detailProject = $state('');

  let editingProject = $state(null);
  let newProjectName = $state('');

  let isRunning = $state(false);

  let searchFilter = $state('');
  let slaOnly = $state(false); // "Has SLA": only projects the dashboard knows an SLA for
  // own keys only: project.core comes from composer.lock, so "constructor" must not hit Object.prototype
  const coreOf = (p) => (p.core && Object.hasOwn(coreStatus, p.core) ? coreStatus[p.core] : null);
  // projects passing the text filter and the "Has SLA" switch
  const visible = $derived.by(() => {
    const q = searchFilter.toLowerCase();
    return projects.filter(p => (!q || p.name.toLowerCase().includes(q)) && (!slaOnly || dashInfo[p.name]?.sla));
  });
  // the list is grouped by what needs doing; "Up to date" starts folded away
  const STATUS_TEXT = { idle: 'Waiting', running: 'Running', success: 'Done', failed: 'Failed', cancelled: 'Cancelled', skipped: 'Skipped' };
  const GROUPS = [
    { key: 'security', label: 'Security update' },
    { key: 'behind', label: 'Update available' },
    { key: 'current', label: 'Up to date' },
    { key: 'unknown', label: 'Not checked' },
  ];
  // a module security update outranks a current core: the site still needs a security update
  const groupOf = (p) => {
    const d = dashInfo[p.name];
    if (d?.securityKnown && d.securityUpdates?.length) return 'security';
    return coreOf(p)?.status || 'unknown';
  };
  const grouped = $derived(GROUPS.map(g => ({ ...g, items: visible.filter(p => groupOf(p) === g.key) })).filter(g => g.items.length));
  let folded = $state({ current: true });
  function toggleGroup(items) {
    const all = items.every(p => selected[p.name]);
    items.forEach(p => selected[p.name] = !all);
  }
  const selectedCount = $derived(projects.filter(p => selected[p.name]).length);
  const hiddenSelected = $derived(selectedCount - visible.filter(p => selected[p.name]).length);

  let branchNames = $state({});
  let confirmResults = $state({});
  let prevView = $state('');
  let merges = $state({}); // project -> {state: working|done|failed, message, url}
  let repoSaved = $state({}); // project -> result of writing drupal-updater.yaml
  let version = $state('');
  let configError = $state('');
  let loading = $state(true);
  let saveError = $state('');
  let justSaved = $state(false);
  // the mode switch saves by itself, so it does not count as an unsaved edit
  const configKey = (c) => JSON.stringify({ ...c, UpdateMode: undefined });
  let snapshot = $state('');
  const dirty = $derived(snapshot !== '' && configKey(config) !== snapshot);
  let loadError = $state('');
  let update = $state(null); // newer release {version, url, canInstall}
  let updateMsg = $state('');

  onMount(async () => {
    // Register event listeners once
    EventsOn('status-update', (update) => {
      const name = update.project || update.Project || update.ProjectName;
      if (!name) return;
      if (update.screen) { screens[name] = update.screen; return; } // live view, not a log line
      const status = update.status || update.Status;
      statuses[name] = status;
      // Pre-fill the branch name from the project's pattern as soon as it succeeds (backend skips names already taken)
      if (status === 'skipped') confirmResults[name] = '– nothing to update'; // no branch left behind, nothing to confirm
      if (status === 'success' && !branchNames[name]) {
        FinalBranch(name).then(b => { if (!branchNames[name]) branchNames[name] = b; }).catch(() => {});
      }
      const cmd = update.command || update.Command;
      if (cmd === 'discover') notStarted[name] = true; // never ran: no temp branch of this run to clean up
      const out = update.output || update.Output;
      if (out && ['success', 'failed', 'skipped', 'cancelled'].includes(status)) {
        results[name] = { status, text: out };
        logs[name] += '→ ' + (STATUS_TEXT[status] || status) + '\n';
      } else if (out) {
        logs[name] += cmd + '\n' + out + '\n';
      } else if (cmd) {
        logs[name] += '$ ' + cmd + '\n';
      }
    });

    EventsOn('merge-update', (u) => { merges[u.project] = u; });
    EventsOn('update-available', (r) => { update = r; });
    version = await GetVersion();
    GetConfigError().then(m => configError = m || '').catch(() => {});
    update = (await GetUpdate()) || update;

    let loaded;
    try {
      loaded = await GetConfig();
      claudeDefaults = await GetClaudeDefaults();
    } catch (e) {
      loadError = 'Could not load settings: ' + (e?.message || e);
      loading = false;
      return;
    }
    config = {
      ...loaded, // keep keys this screen doesn't edit
      UpdateMode: loaded.UpdateMode || 'standard',
      ClaudeModel: loaded.ClaudeModel || 'sonnet',
      ClaudePermissionMode: loaded.ClaudePermissionMode || 'dontAsk',
      ClaudeTimeoutMinutes: loaded.ClaudeTimeoutMinutes || 60,
      ClaudeAllowedTools: loaded.ClaudeAllowedTools?.length ? loaded.ClaudeAllowedTools : [...claudeDefaults.allowedTools],
      ClaudePrompt: loaded.ClaudePrompt || claudeDefaults.prompt,
      BaseFolders: loaded.BaseFolders || [],
      ProjectPaths: loaded.ProjectPaths || [],
      MaxConcurrent: loaded.MaxConcurrent || 3,
      ScanDepth: loaded.ScanDepth || 1,
      ExcludedProjects: loaded.ExcludedProjects || [],
      Projects: loaded.Projects || {},
      Defaults: loaded.Defaults || {},
      DashboardURL: loaded.DashboardURL || DASH_DEFAULT,
    };
    snapshot = configKey(config);
    GetPromptMerge().then(m => { promptMerge = m; showMerge = !!m; }).catch(() => {});
    await loadProjects();
  });

  // custom prompt vs a changed built-in prompt: merged in a dialog (now, or later from Settings)
  let promptMerge = $state(null);
  let showMerge = $state(false);
  async function savePromptMerge(text) {
    await ResolvePromptMerge(text); // throws with the reason, shown in the dialog
    const clean = !dirty;
    const l = await GetConfig();
    config.ClaudePrompt = l.ClaudePrompt || claudeDefaults.prompt;
    config.ClaudePromptBase = l.ClaudePromptBase;
    if (clean) snapshot = configKey(config);
    promptMerge = null;
    showMerge = false;
  }

  let coreStatus = $state({});
  const CORE_TITLE = {
    current: () => 'Drupal core is up to date',
    behind: (r) => `Drupal core is behind (${r.latest} available), no security update`,
    security: (r) => `Security update needed (${r.latest} available)`,
  };
  // Applications dashboard (optional): SLA, customer and pending security modules per project
  const DASH_DEFAULT = 'https://applications.dyna-ops.be/api/mcp'; // the backend uses it too when the address is empty
  let dash = $state({ configured: false, signedIn: false, server: '' });
  let dashInfo = $state({});
  let dashCand = $state({ projects: [], unmatched: [] });
  const autoMatches = $derived(Object.entries(dashInfo).filter(([, d]) => d.autoMatched).sort(([x], [y]) => x.localeCompare(y)));
  let dashPick = $state({}); // per local project: result of writing its dashboard_project
  let dashBusy = $state(false);
  let dashError = $state('');
  async function refreshDashboard(force = false) {
    try { dash = await DashboardState(); } catch { return; }
    if (!dash.configured || !dash.signedIn) { dashInfo = {}; return; }
    dashBusy = true;
    try {
      dashInfo = (await DashboardInfo(force)) || {};
      dashCand = (await DashboardCandidates()) || { projects: [], unmatched: [] };
      dashError = '';
    }
    catch (e) {
      const msg = String(e?.message || e);
      if (msg === 'signed-out') { dash = { ...dash, signedIn: false }; dashInfo = {}; }
      else dashError = msg;
    } finally { dashBusy = false; }
  }
  // Point a local project at a dashboard project: written to the project's drupal-updater.yaml (commit it to
  // share it with the team); the repo file wins over local settings, so nothing else needs saving.
  async function pickDashboard(local, ref) {
    dashPick[local] = 'saving…';
    try {
      await SaveProjectFile(local, { DashboardProject: ref });
      dashPick[local] = '✓ saved in drupal-updater.yaml, commit it to share';
    } catch (e) {
      dashPick[local] = '✗ ' + (e?.message || e);
      return;
    }
    await refreshDashboard(false);
  }
  async function dashSignIn() {
    dashBusy = true; dashError = '';
    try { await DashboardSignIn(); } catch (e) { dashError = String(e?.message || e); }
    dashBusy = false;
    await refreshDashboard(true);
  }
  async function dashSignOut() {
    try { await DashboardSignOut(); } catch (e) { dashError = String(e?.message || e); }
    await refreshDashboard();
  }
  // Detail card for a project's dashboard data: opens at once on hover or focus, a click pins it
  let pop = $state(null); // { local, rect, pinned }
  let popTimer;
  function openPop(e, local, pinned = false) {
    clearTimeout(popTimer);
    pop = { local, rect: e.currentTarget.getBoundingClientRect(), pinned: pinned || (pop?.pinned && pop.local === local) };
  }
  function togglePin(e, local) {
    e.preventDefault(); e.stopPropagation(); // the pill sits inside the row's label: do not toggle the checkbox
    if (pop?.pinned && pop.local === local) pop = null;
    else openPop(e, local, true);
  }
  function schedulePopClose() {
    if (pop?.pinned) return;
    clearTimeout(popTimer);
    popTimer = setTimeout(() => pop = null, 180); // long enough to move the pointer into the card
  }
  function closePop() { clearTimeout(popTimer); pop = null; }

  $effect(() => { if (slaOnly && Object.keys(dashInfo).length === 0) slaOnly = false; });

  async function loadProjects() {
    loading = true; loadError = '';
    try {
      projects = await DiscoverProjects();
      projects.forEach(p => {
        if (!(p.name in selected)) selected[p.name] = false;
      });
    } catch (e) {
      projects = [];
      loadError = 'Could not scan for projects: ' + (e?.message || e);
    } finally { loading = false; }
    refreshDashboard();
    await updateStatuses();
    refreshCores(false); // switches to the version committed on origin, fetching only when the last fetch is over a day old
  }
  // dots are optional: offline or a failed lookup just leaves them out
  async function updateStatuses() {
    try { coreStatus = (await CoreStatuses([...new Set(projects.map(p => p.core).filter(Boolean))])) || {}; } catch {}
  }
  let refreshing = $state(false);
  let refreshDone = $state(0);
  let coreNote = $state('');
  // Re-reads every project's core version from origin's start branch, 4 projects at a time.
  // force = the Refresh button: fetch everything now and download Drupal's release list again.
  async function refreshCores(force) {
    if (refreshing || projects.length === 0) return;
    refreshing = true; refreshDone = 0; coreNote = '';
    try {
      if (force) await RefreshReleases();
      const queue = [...projects];
      const found = {};
      let failed = 0;
      const worker = async () => {
        for (let p; (p = queue.shift()); refreshDone++) {
          try {
            const r = await RefreshCore(p.name, force);
            if (r.version) found[p.name] = r.version;
            if (r.fetchFailed) failed++;
          } catch {}
        }
      };
      await Promise.all([worker(), worker(), worker(), worker()]);
      projects.forEach(p => { if (found[p.name]) p.core = found[p.name]; }); // one pass, not one re-filter per project
      await updateStatuses();
      const notes = [];
      if (failed) notes.push(`${failed} ${failed === 1 ? 'project' : 'projects'} could not be fetched (offline?), versions may be old`);
      if (projects.some(p => p.core) && Object.keys(coreStatus).length === 0) notes.push("Drupal's release list is unavailable, so there are no status dots");
      coreNote = notes.join('. ');
    } finally { refreshing = false; }
  }

  function doStart() {
    // running projects and ones with a merge request in flight are left alone; every other row keeps its state
    const active = n => statuses[n] === 'running' || statuses[n] === 'idle' || merges[n]?.state === 'working' || confirmResults[n] === '…';
    const selectedNames = projects.filter(p => selected[p.name] && !active(p.name)).map(p => p.name);
    if (selectedNames.length === 0) return;
    selectedNames.forEach(resetRow);

    view = 'running';
    isRunning = true;

    StartUpdate({ projects: selectedNames });
  }

  async function saveConfig() {
    // Store defaults as empty so future built-in improvements still apply.
    const toSave = { ...config };
    if (toSave.ClaudePrompt === claudeDefaults.prompt) toSave.ClaudePrompt = '';
    if (JSON.stringify(toSave.ClaudeAllowedTools) === JSON.stringify(claudeDefaults.allowedTools)) toSave.ClaudeAllowedTools = [];
    saveError = '';
    try {
      await SaveConfig(toSave);
      promptMerge = await GetPromptMerge().catch(() => promptMerge); // editing the prompt here settles it
    } catch (e) {
      saveError = 'Could not save: ' + (e?.message || e);
      return;
    }
    snapshot = configKey(config);
    justSaved = true;
    setTimeout(() => justSaved = false, 2500);
    await loadProjects(); // folders or the dashboard address may have changed; stay on the settings page
  }

  function addProject() {
    if (newProjectName && !config.Projects[newProjectName]) {
      config.Projects[newProjectName] = {
        GitStartBranch: '', // empty = branch defaults
        GitNewBranchPattern: '',
        IgnoredPackages: [],
        SmokePaths: [],
      };
      newProjectName = '';
    }
  }

  function deleteProject(name) {
    delete config.Projects[name];
    if (editingProject === name) editingProject = null;
  }

  const finished = n => ['success', 'failed', 'cancelled', 'skipped'].includes(statuses[n]);
  const done = n => !!confirmResults[n] && !confirmResults[n].startsWith('✗'); // '…' = in flight
  const busy = $derived(Object.values(statuses).some(s => s === 'running' || s === 'idle'));

  // Clear a row so it can be (re)started.
  function resetRow(name) {
    statuses[name] = 'idle';
    logs[name] = '';
    for (const o of [results, screens, confirmResults, merges, branchNames, notStarted]) delete o[name];
  }

  // Rename the finished project's branch, or clean it up when it failed. Other projects are untouched.
  async function confirmProject(name) {
    if (confirmResults[name] === '…') return;
    confirmResults[name] = '…';
    try {
      if (notStarted[name]) {
        confirmResults[name] = '– not started';
      } else if (statuses[name] === 'success') {
        await RenameBranch(name, branchNames[name]);
        confirmResults[name] = '✓ renamed';
      } else {
        await CleanupBranch(name);
        confirmResults[name] = '✓ cleaned up';
      }
    } catch (e) {
      confirmResults[name] = '✗ ' + (e?.message || e);
    }
  }

  function confirmAll() {
    Object.keys(statuses).filter(n => finished(n) && !done(n) && !notStarted[n]).forEach(confirmProject);
  }

  async function installUpdate() {
    updateMsg = 'Downloading ' + update.version + '…';
    try {
      await InstallUpdate(); // the app restarts on success
    } catch (e) {
      updateMsg = '✗ ' + (e?.message || e);
    }
  }

  const renamed = name => confirmResults[name] === '✓ renamed';

  function openMergeRequest(name) {
    merges[name] = { state: 'working', message: 'starting…' };
    MergeRequest(name, branchNames[name]);
  }

  async function saveToRepo(name) {
    try {
      repoSaved[name] = '✓ wrote ' + await SaveProjectFile(name, config.Projects[name]) + ' — commit it';
      await loadProjects();
    } catch (e) {
      repoSaved[name] = '✗ ' + (e?.message || e);
    }
  }

  function retryFailed() {
    const failed = Object.keys(statuses).filter(name =>
      (statuses[name] === 'failed' || statuses[name] === 'cancelled') && !done(name)
    );
    if (failed.length === 0) return;
    failed.forEach(resetRow);
    view = 'running';
    RetryProjects(failed);
  }

  function retryProject(name) {
    resetRow(name);
    RetryProjects([name]);
  }

  // Remove a handled project from the list; leave the running view when nothing is left.
  function dismiss(name) {
    for (const o of [statuses, logs, results, screens, confirmResults, merges, branchNames, notStarted]) delete o[name];
    if (Object.keys(statuses).length === 0) {
      isRunning = false;
      view = 'projects';
    }
  }
</script>

{#if welcome}<Welcome bind:theme bind:font bind:fontSize ondone={finishWelcome} />{/if}
{#if showMerge && promptMerge && !welcome}<PromptMerge merge={promptMerge} onsave={savePromptMerge} onlater={() => showMerge = false} />{/if}
<Tour bind:this={tour} go={goTo} bind:active={tourActive} />

<svelte:window onpointerdown={(e) => { if (pop?.pinned && !e.target.closest?.('.pop, .info')) closePop(); }} />
{#if pop}
  {@const pp = projects.find(p => p.name === pop.local)}
  <DashPopover info={dashInfo[pop.local]} project={pp} status={pp ? coreOf(pp) : null} rect={pop.rect} pinned={pop.pinned}
    onenter={() => clearTimeout(popTimer)} onleave={schedulePopClose} onclose={closePop} onsave={() => pickDashboard(pop.local, dashInfo[pop.local]?.ref)} />
{/if}

<main class:mac={isMac} inert={welcome || tourActive}>
  {#if isMac}<div class="titlebar"></div>{/if}
  <header>
    <h1><svg class="spark" viewBox="0 0 45 36" aria-hidden="true"><path fill="currentColor" d="M44.3405 17.295L44.3252 17.2935C30.4154 17.3718 24.506 9.22934 23.2001 1.35592C23.0692 0.566652 21.923 0.572046 21.7632 1.35592L21.7548 1.40223C20.4219 9.2372 14.5201 17.3076 0.704589 17.2534L0.659481 17.2546C-0.21725 17.3507 -0.2224 18.7116 0.659481 18.7066C14.5693 18.6283 20.4787 26.7708 21.7846 34.6442C21.9155 35.4334 23.0617 35.428 23.2215 34.6442L23.2299 34.5979C24.5647 26.7515 30.4818 18.669 44.3405 18.747C45.2224 18.7519 45.2172 17.391 44.3405 17.295Z"/></svg>Drupal Updater</h1>
    {#if update}
      <div class="update-banner">
        <span>{updateMsg || `${update.version} available`}</span>
        {#if update.canInstall && !updateMsg.startsWith('Downloading')}
          <button class="btn-sm" onclick={installUpdate} disabled={busy || Object.values(confirmResults).includes('…') || Object.values(merges).some(m => m?.state === 'working')}>Update &amp; restart</button>
        {/if}
        <button class="btn-sm" onclick={() => BrowserOpenURL(update.url)}>Release notes ↗</button>
      </div>
    {:else if version}
      <span class="version">{version}</span>
    {/if}
    <nav>
      <button class:active={view === 'projects'} onclick={() => view = 'projects'}>Projects</button>
      <button class:active={view === 'config'} onclick={() => view = 'config'}>Settings</button>
      <button data-tour="help" class="help" title="Show the tutorial" aria-label="Show the tutorial" disabled={busy} onclick={() => tour.start()}>?</button>
      {#if isRunning}
        <button class:active={view === 'running'} onclick={() => view = 'running'}>
          {busy ? '● Running' : '⚑ Review'}
        </button>
      {/if}
    </nav>
  </header>

  {#if configError}<p class="notice" role="alert">{configError}</p>{/if}

  {#if view === 'projects'}
    <section class="panel">
      <div class="panel-header" data-tour="filters">
        <h2 class="sr">Select projects to update</h2>
        <div class="search-box">
          <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" bind:value={searchFilter} placeholder="Filter projects..." class="search-input" />
        </div>
        {#if coreNote}<span class="hint core-note" title={coreNote}>⚠ {coreNote}</span>{/if}
        {#if Object.keys(dashInfo).length}
          <label class="sla-only" title="Only projects with a maintenance contract (SLA) in the dashboard">
            <input type="checkbox" bind:checked={slaOnly} /> Has SLA
          </label>
        {/if}
        <div class="actions">
          <button class="btn-sm" onclick={() => refreshCores(true)} disabled={refreshing} title="Fetch every project's origin and check Drupal's latest releases again">{refreshing ? `Refreshing ${refreshDone}/${projects.length}` : 'Refresh'}</button>
        </div>
      </div>

      <div class="project-list" data-tour="projects" onscroll={closePop}>
        {#each grouped as g (g.key)}
          {@const open = !folded[g.key]}
          {@const allOn = g.items.every(p => selected[p.name])}
          <div class="group group-{g.key}">
            <button type="button" class="group-toggle" aria-expanded={open} onclick={() => folded[g.key] = open}>
              <span class="chev" aria-hidden="true">{open ? '▾' : '▸'}</span>{g.label} <span class="count">{g.items.length}</span>
            </button>
            <button type="button" class="group-select" onclick={() => toggleGroup(g.items)}>{allOn ? 'Deselect all' : 'Select all'}</button>
          </div>
          {#if open}
            <ul>
              {#each g.items as project (project.name)}
                {@const st = coreOf(project)}
                {@const d = dashInfo[project.name]}
                <li class:selected={selected[project.name]}>
                  <label>
                    <input type="checkbox" bind:checked={selected[project.name]} />
                    <span class="namecell">
                      <span class="name">{project.name}</span>
                      {#if project.repoFile}<span class="repo-mark" title="Has a drupal-updater.yaml in the repository">✓<span class="sr">repo config</span></span>{/if}
                      {#if d?.sla}<span class="sla-mark" title="Has an SLA">SLA</span>{/if}
                    </span>
                    <span class="versions mono">
                      {#if project.core && st && st.status !== 'current'}
                        <span class="from">{project.core}</span><span class="arrow" aria-hidden="true">→</span><span class="target">{st.latest}</span>
                        <span class="sr">{CORE_TITLE[st.status]?.(st)}</span>
                      {:else if project.core}
                        <span class="from">{project.core}</span>
                      {/if}
                    </span>
                    <span class="mods">{#if d?.securityKnown && d.securityUpdates?.length}{d.securityUpdates.length} {d.securityUpdates.length === 1 ? 'module' : 'modules'}<span class="sr"> with security updates</span>{/if}</span>
                  </label>
                  <button type="button" class="info" aria-label="Details for {project.name}" aria-haspopup="dialog" aria-expanded={pop?.local === project.name}
                    onmouseenter={(e) => openPop(e, project.name)} onmouseleave={schedulePopClose} onfocus={(e) => openPop(e, project.name)} onblur={schedulePopClose}
                    onclick={(e) => togglePin(e, project.name)}>i</button>
                </li>
              {/each}
            </ul>
          {/if}
        {/each}
      </div>

      {#if projects.length > 0 && visible.length === 0}
        <p class="empty">No projects match the filters.</p>
      {/if}

      {#if loading}
        <p class="empty">Looking for projects…</p>
      {:else if loadError}
        <p class="notice" role="alert">{loadError}</p>
      {:else if projects.length === 0}
        <p class="empty">No projects found. Check your project folders in Settings.</p>
      {/if}

      <div class="footer-actions">
        <div class="update-mode" data-tour="mode">
          <span>Mode</span>
          <button type="button" tabindex="-1" class="mode-label" class:on={config.UpdateMode !== 'disaster'} onclick={() => setMode('standard')}>Standard</button>
          <button type="button" role="switch" class="mode-switch" aria-label="Disaster mode (security fixes only)" aria-checked={config.UpdateMode === 'disaster'} onclick={() => setMode(config.UpdateMode === 'disaster' ? 'standard' : 'disaster')}><span class="knob"></span></button>
          <button type="button" tabindex="-1" class="mode-label" class:on={config.UpdateMode === 'disaster'} onclick={() => setMode('disaster')}>Disaster</button>
          <span class="mode-hint">{config.UpdateMode === 'disaster' ? 'security fixes only' : 'all minor and patch updates'}</span>
        </div>
        <button class="btn-primary" data-tour="start" onclick={doStart} disabled={!selectedCount}>
          {selectedCount ? `Update ${selectedCount} ${selectedCount === 1 ? 'project' : 'projects'}` : 'Select projects to update'}{hiddenSelected > 0 ? ` (${hiddenSelected} hidden)` : ''}
        </button>
      </div>
    </section>

  {:else if view === 'config'}
    <section class="panel config-panel">
      <div class="settings-layout">
        <nav class="settings-tabs" role="tablist" aria-orientation="vertical" onkeydown={segKeys}>
          {#each TABS as [key, label]}
            <button type="button" role="tab" id="tab-{key}" aria-controls="panel-{key}" tabindex={settingsTab === key ? 0 : -1} aria-selected={settingsTab === key} class:on={settingsTab === key} onclick={() => settingsTab = key}>{label}</button>
          {/each}
        </nav>
        <div class="settings-content">
      <div class="settings-section" role="tabpanel" id="panel-dashboard" data-tour="dashboard" aria-labelledby="tab-dashboard" hidden={settingsTab !== 'dashboard'}>
        <div class="section-head">
          <h3>Dashboard</h3>
          <p class="hint">Optional. Shows each project's SLA and a link to its page in the applications dashboard. Read-only: nothing is written back.</p>
        </div>
        <div class="section-body">
          <label class="field">
            Dashboard address <span class="hint">the server root or the full MCP address</span>
            <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" placeholder="https://applications.example.com" bind:value={config.DashboardURL} />
          </label>
          {#if dirty}<p class="hint">Save the settings first: Connect uses the saved address.</p>{/if}
          <div class="dash-state">
            {#if !dash.configured}
              <span class="hint err">{dash.error || 'Not configured.'}</span>
            {:else if dash.signedIn}
              <span class="dash-ok">● Connected to {dash.server}</span>
              <button class="btn-sm" onclick={() => refreshDashboard(true)} disabled={dashBusy}>{dashBusy ? 'Loading…' : 'Reload'}</button>
              <button class="btn-sm" onclick={dashSignOut} disabled={dashBusy}>Disconnect</button>
            {:else}
              <span class="hint">Not connected.</span>
              <button class="btn-sm" onclick={dashSignIn} disabled={dashBusy}>{dashBusy ? 'Waiting for the browser…' : 'Connect…'}</button>
            {/if}
          </div>
          {#if dashError}<p class="notice" role="alert">{dashError}</p>{/if}
          {#if dash.signedIn}<p class="hint">{Object.keys(dashInfo).length} of {projects.length} projects matched a dashboard project ({autoMatches.length} of them detected automatically). Data from the dashboard comes from scans and connectors on each site and can be days old; the Drupal version and status dots in the list are always read from the repository.</p>{/if}
          {#if dash.signedIn && autoMatches.length}
            <div class="field">
              <span>Detected automatically ({autoMatches.length}) <span class="hint">matched from the git group, DDEV and site names; save the ones that are right to make them permanent</span></span>
              <ul class="unmatched">
                {#each autoMatches as [local, d] (local)}
                  <li>
                    <span class="local" title={local}>{local} → {d.name}</span>
                    <button type="button" class="btn-sm" onclick={() => pickDashboard(local, d.ref)}>Save match</button>
                    <span class="hint why-line">because {d.matchReason}</span>
                    {#if dashPick[local]}<span class="hint pick">{dashPick[local]}</span>{/if}
                  </li>
                {/each}
              </ul>
            </div>
          {/if}
          {#if dash.signedIn && dashCand.unmatched.length}
            <div class="field">
              <span>Unmatched projects ({dashCand.unmatched.length}) <span class="hint">pick the dashboard project each one is; it is written to that project's drupal-updater.yaml, which you commit to share it</span></span>
              <ul class="unmatched">
                {#each dashCand.unmatched as u (u.local)}
                  <li>
                    <span class="local" title={u.local}>{u.local}</span>
                    <select aria-label="Dashboard project for {u.local}" onchange={(e) => { const v = e.currentTarget.value; e.currentTarget.value = ''; if (v) pickDashboard(u.local, v); }}>
                      <option value="">{u.suggestions.length ? 'Other…' : 'Choose…'}</option>
                      {#each dashCand.projects as p (p.ref)}<option value={p.ref}>{p.name}{p.customer ? ' · ' + p.customer : ''}</option>{/each}
                    </select>
                    <span class="sugg">
                      {#each u.suggestions as sug (sug.ref)}
                        <button type="button" class="btn-sm" title={sug.customer || ''} onclick={() => pickDashboard(u.local, sug.ref)}>{sug.name}</button>
                      {/each}
                    </span>
                    {#if dashPick[u.local]}<span class="hint pick">{dashPick[u.local]}</span>{/if}
                  </li>
                {/each}
              </ul>
            </div>
          {/if}
        </div>
      </div>

      <div class="settings-section" role="tabpanel" id="panel-appearance" aria-labelledby="tab-appearance" hidden={settingsTab !== 'appearance'}>
        <div class="section-head">
          <h3>Appearance</h3>
          <p class="hint">Applies immediately, stored on this machine.</p>
        </div>
        <div class="section-body">
          <Seg label="Theme" items={THEMES} value={theme} onpick={v => theme = v} />
          <Seg label="Font" items={FONT_ITEMS} value={font} onpick={v => font = v} />
          <Seg label="Font size" items={SIZES} value={fontSize} onpick={v => fontSize = v} />
          <div class="field">
            <span>Tutorial</span>
            <button class="btn-sm replay" onclick={() => tour.start()}>Show tutorial again</button>
          </div>
        </div>
      </div>

      <div class="settings-section" role="tabpanel" id="panel-projects" aria-labelledby="tab-projects" hidden={settingsTab !== 'projects'} data-tour="folders">
        <div class="section-head">
          <h3>Projects</h3>
          <p class="hint">Where Drupal projects are found.</p>
        </div>
        <div class="section-body">
          <div class="section-body two-col">
            <Slider label="Scan depth" value={config.ScanDepth} onchange={v => config.ScanDepth = v} min={1} max={Math.max(4, config.ScanDepth || 1)} format={v => v === 1 ? '1 level' : v + ' levels'} describe={depthText} />
            <Slider label="Max concurrent runs" value={config.MaxConcurrent} onchange={v => config.MaxConcurrent = v} min={1} max={Math.max(10, config.MaxConcurrent || 1)} gradient tone={loadHint} describe={loadText} />
          </div>
          <div class="field">
            <span>Base folders <span class="hint">each is searched for Drupal projects (see scan depth)</span></span>
            <ListEditor label="Base folders" items={config.BaseFolders} onchange={v => config.BaseFolders = v} placeholder="~/projects" onbrowse={PickFolder} emptyText="No base folders yet" />
          </div>
          <div class="field">
            <span>Extra project paths <span class="hint">single projects outside the base folders</span></span>
            <ListEditor label="Extra project paths" items={config.ProjectPaths} onchange={v => config.ProjectPaths = v} placeholder="~/work/one-off-site" onbrowse={PickFolder} emptyText="None" />
          </div>
          <div class="field">
            <span>Excluded projects <span class="hint">folder names, <code>*</code> is a wildcard</span></span>
            <ListEditor label="Excluded projects" items={config.ExcludedProjects} onchange={v => config.ExcludedProjects = v} placeholder="sandbox*" emptyText="Nothing excluded" />
          </div>
          <div class="field">
            <span>Multisite <span class="hint">off: drush runs for the site in <code>.drush_subsite</code>, or the default site · on: for every site ddev reaches, with <code>--uri</code>. Project overrides win.</span></span>
            <Seg label="Multisite" labelled={false} items={[['', 'Off'], ['on', 'On']]} value={config.Defaults.Multisite === 'on' ? 'on' : ''} onpick={v => config.Defaults.Multisite = v} />
          </div>
          {#if previewCount !== null}
            <p class="hint preview" aria-live="polite">{previewCount === 0 ? 'No projects found with these settings.' : `These settings find ${previewCount} ${previewCount === 1 ? 'project' : 'projects'}.`}</p>
          {/if}
        </div>
      </div>

      <div class="settings-section" role="tabpanel" id="panel-branches" aria-labelledby="tab-branches" hidden={settingsTab !== 'branches'}>
        <div class="section-head">
          <h3>Branch defaults</h3>
          <p class="hint">For every project. Project overrides and a repo's <code>drupal-updater.yaml</code> win.</p>
        </div>
        <div class="section-body two-col">
          <div class="field span-2">
            <span>Start branch <span class="hint">the first one that exists in a repo is used; the others are fallbacks</span></span>
            <ListEditor ordered label="Start branch" items={branches(config.Defaults.GitStartBranch)} onchange={v => config.Defaults.GitStartBranch = v.join('|')} placeholder="master" emptyText="Empty: master, then main" />
          </div>
          <label class="field">
            Final branch pattern <span class="hint">{'{date}'}, * = project</span>
            <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" placeholder="hotfix/{'{date}'}-drupal-updates" bind:value={config.Defaults.GitNewBranchPattern} />
          </label>
          <label class="field">
            Merge request target
            <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" placeholder="the start branch" bind:value={config.Defaults.GitMergeTarget} />
          </label>
          <label class="field">
            Commit message
            <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" placeholder="Drupal updates {'{date}'}" bind:value={config.Defaults.GitCommitMessage} />
          </label>
        </div>
      </div>

      <div class="settings-section" role="tabpanel" id="panel-claude" aria-labelledby="tab-claude" hidden={settingsTab !== 'claude'} data-tour="claude">
        <div class="section-head">
          <h3>Claude</h3>
          <p class="hint">The session that runs each update.</p>
        </div>
        <div class="section-body three-col">
          <label class="field">
            Model
            <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" bind:value={config.ClaudeModel} />
          </label>
          <label class="field">
            Permission mode
            <select bind:value={config.ClaudePermissionMode}>
              <option value="dontAsk">dontAsk (deny unlisted tools)</option>
              <option value="acceptEdits">acceptEdits</option>
              <option value="plan">plan</option>
              <option value="manual">manual</option>
            </select>
          </label>
          <Slider label="Timeout" value={config.ClaudeTimeoutMinutes} onchange={v => config.ClaudeTimeoutMinutes = v} min={5} max={Math.max(240, config.ClaudeTimeoutMinutes || 60)} step={5} format={v => v + ' min'} describe={() => 'A run that takes longer is stopped; work already committed is kept on a failed- branch.'} />
          <details class="advanced span-3">
            <summary>Advanced: allowed tools and prompt</summary>
            <label class="field">
              Allowed tools <span class="hint">one per line</span>
              <textarea autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" rows="5" value={config.ClaudeAllowedTools?.join('\n') || ''}
                onchange={(e) => config.ClaudeAllowedTools = e.target.value.split('\n').map(s => s.trim()).filter(Boolean)}></textarea>
            </label>
            <label class="field">
              Prompt
              <span class="hint">
                <Explain label="Show placeholders" title="Prompt placeholders">
                  <p>The app replaces these in the prompt before Claude starts.</p>
                  <dl>
                  <dt>{'{{setup}}'}</dt><dd>What the app already did before Claude starts (checkout, fetch, temp branch, ddev start, composer install, drush deploy), plus the output of the first step that failed, which Claude then finishes itself.</dd>
                  <dt>{'{{update_list}}'}</dt><dd>What there is to update, as found by the app: the open security advisories (disaster mode) or the minor and patch updates (standard mode). Required: a prompt without it is replaced by the built-in one.</dd>
                  <dt>{'{{sites}}'}</dt><dd>The sites the app runs drush and its checks for: the default site, the subsite in <code>.drush_subsite</code>, or with Multisite on every site ddev reaches, with its URL for <code>--uri</code> (and the sites it had to skip).</dd>
                  <dt>{'{{updates}}'}</dt><dd>The update rules of the current update mode (disaster: security fixes only; standard: minor and patch updates, core first).</dd>
                  <dt>{'{{previous_failure}}'}</dt><dd>Why the previous run of this project failed, so it is not repeated; empty when the last run did not fail.</dd>
                  <dt>{'{{project}}'}</dt><dd>The project's folder name.</dd>
                  <dt>{'{{path}}'}</dt><dd>The project folder; Claude's working directory.</dd>
                  <dt>{'{{start_branch}}'}</dt><dd>The branch the update starts from.</dd>
                  <dt>{'{{temp_branch}}'}</dt><dd>The branch the update is made on; renamed to the final branch when you confirm.</dd>
                  <dt>{'{{ignored_packages}}'}</dt><dd>Packages that must not be updated (project overrides), or "none".</dd>
                  <dt>{'{{project_instructions}}'}</dt><dd>The project's extra Claude instructions (project overrides), or nothing.</dd>
                  <dt>{'{{smoke_paths}}'}</dt><dd>The project's extra smoke-test paths, or "none". The app tests them itself; only useful if Claude should look at them too.</dd>
                  <dt>{'{{result_file}}'}</dt><dd>Where Claude writes its result (status and summary). Keep this in the prompt.</dd>
                  </dl>
                </Explain>
              </span>
              <textarea autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" rows="14" bind:value={config.ClaudePrompt}></textarea>
            </label>
            <div>
              <button class="btn-sm" onclick={() => config.ClaudePrompt = claudeDefaults.prompt}>Reset prompt to default</button>
              {#if promptMerge}<button class="btn-sm" onclick={() => showMerge = true}>Merge with the new default…</button>{/if}
            </div>
          </details>
        </div>
      </div>

      <div class="settings-section" role="tabpanel" id="panel-overrides" aria-labelledby="tab-overrides" hidden={settingsTab !== 'overrides'}>
        <div class="section-head">
          <h3>Project overrides</h3>
          <p class="hint">Local to this machine. A committed <code>drupal-updater.yaml</code> wins per field; "Save to repo" writes one for the team.</p>
        </div>
        <div class="section-body">
          <div class="project-configs">
            {#each Object.keys(config.Projects || {}) as name}
              <div class="project-config-item">
                <div class="project-config-header">
                  <strong>{name}</strong>
                  {#if projects.find(p => p.name === name)?.repoFile}<span class="badge badge-repo">repo config</span>{/if}
                  {#if repoSaved[name]}<span class="confirm-result">{repoSaved[name]}</span>{/if}
                  <div>
                    <button class="btn-sm" onclick={() => saveToRepo(name)} disabled={!projects.some(p => p.name === name)}>Save to repo</button>
                    <button class="btn-sm" onclick={() => editingProject = editingProject === name ? null : name}>
                      {editingProject === name ? 'Close' : 'Edit'}
                    </button>
                    <button class="btn-sm btn-danger" onclick={() => deleteProject(name)}>Delete</button>
                  </div>
                </div>

                {#if editingProject === name}
                  <div class="project-config-edit">
                    <div class="edit-field">
                      <span>Start branch <span class="hint">empty = the defaults ({config.Defaults.GitStartBranch || 'master|main'})</span></span>
                      <ListEditor ordered label="Start branch for {name}" items={branches(config.Projects[name].GitStartBranch)} onchange={v => config.Projects[name].GitStartBranch = v.join('|')} placeholder={config.Defaults.GitStartBranch || 'master'} emptyText="Uses the defaults" />
                    </div>
                    <label>
                      Branch pattern
                      <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" placeholder={config.Defaults.GitNewBranchPattern || 'hotfix/{date}-drupal-updates'} bind:value={config.Projects[name].GitNewBranchPattern} />
                    </label>
                    <label>
                      Merge request target
                      <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" placeholder={config.Defaults.GitMergeTarget || 'the start branch'} bind:value={config.Projects[name].GitMergeTarget} />
                    </label>
                    <label>
                      Commit message for the merge request
                      <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" placeholder={config.Defaults.GitCommitMessage || 'Drupal updates {date}'} bind:value={config.Projects[name].GitCommitMessage} />
                    </label>
                    <div class="edit-field">
                      <span>Ignored packages</span>
                      <ListEditor label="Ignored packages for {name}" items={config.Projects[name].IgnoredPackages} onchange={v => config.Projects[name].IgnoredPackages = v} placeholder="drupal/some_module" emptyText="None" />
                    </div>
                    <div class="edit-field">
                      <span>Extra smoke-test paths <span class="hint">prefix <code>admin:</code> for logged-in pages · <Explain label="How the smoke test works" title="The smoke test">
                        <p>The app tests the site itself, once before Claude starts (the baseline) and once after the update, over ddev's local http URL. It checks:</p>
                        <ul>
                          <li>Drupal boots (<code>drush status</code>) and <code>drush cr</code> works</li>
                          <li>anonymous pages: <code>/</code>, <code>/user/login</code>, the newest published node of each content type, and that an unknown page gives a 404</li>
                          <li>admin pages, logged in through <code>drush uli</code>: content, structure, configuration, the status report and the add form of each content type</li>
                          <li>no new Error-level entries in the Drupal log</li>
                        </ul>
                        <p>A page fails when it does not answer as expected or shows a PHP or Drupal error. Only checks that passed before and fail after count; those go to a repair session. Checks that already failed before the update are reported, not fixed.</p>
                        <p><strong>Adding checks</strong></p>
                        <ul>
                          <li><strong>Pages:</strong> add paths here, e.g. <code>/nl/contact</code>. Prefix <code>admin:</code> to check a page while logged in, e.g. <code>admin:/admin/structure/webform</code>.</li>
                          <li><strong>Flows</strong> (submitting a form, a checkout, a search): describe them in the testing or smoke test section of the project's <code>AGENTS.md</code> (or <code>CLAUDE.md</code>). Claude carries them out after updating and fixes what broke.</li>
                          <li><strong>Anything else</strong> for one project: the extra Claude instructions in these overrides.</li>
                        </ul>
                      </Explain></span></span>
                      <ListEditor label="Smoke-test paths for {name}" items={config.Projects[name].SmokePaths} onchange={v => config.Projects[name].SmokePaths = v} placeholder="/contact" emptyText="None" />
                    </div>
                    <div class="edit-field">
                      <span>Multisite <span class="hint">follows the defaults ({config.Defaults.Multisite === 'on' ? 'on' : 'off'}) until you change it here</span></span>
                      <Seg label="Multisite for {name}" labelled={false} items={[['off', 'Off'], ['on', 'On']]}
                        value={config.Projects[name].Multisite || (config.Defaults.Multisite === 'on' ? 'on' : 'off')}
                        onpick={v => config.Projects[name].Multisite = v === (config.Defaults.Multisite === 'on' ? 'on' : 'off') ? '' : v} />
                    </div>
                    <label>
                      Dashboard project <span class="hint">only when its name differs from the folder name</span>
                      <select bind:value={config.Projects[name].DashboardProject}>
                        <option value="">Match by name{dashInfo[name] ? ` (now: ${dashInfo[name].name})` : ''}</option>
                        {#if config.Projects[name].DashboardProject && !dashCand.projects.some(p => p.ref === config.Projects[name].DashboardProject)}<option value={config.Projects[name].DashboardProject}>{config.Projects[name].DashboardProject}</option>{/if}
                        {#each dashCand.projects as p (p.ref)}<option value={p.ref}>{p.name}{p.customer ? ' · ' + p.customer : ''}</option>{/each}
                      </select>
                    </label>
                    <label>
                      Extra instructions for Claude (added to the prompt)
                      <textarea autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" rows="5" bind:value={config.Projects[name].ClaudeInstructions}></textarea>
                    </label>
                  </div>
                {/if}
              </div>
            {/each}

            <div class="add-project">
              <select bind:value={newProjectName}>
                <option value="">Select a project...</option>
                {#each projects.filter(p => !config.Projects[p.name]) as p}
                  <option value={p.name}>{p.name}</option>
                {/each}
              </select>
              <button class="btn-sm" onclick={addProject} disabled={!newProjectName}>+ Add</button>
            </div>
          </div>
        </div>
      </div>

        </div>
      </div>

      <div class="footer-actions">
        {#if saveError}<span class="save-note err" role="alert">{saveError}</span>{:else if dirty}<span class="save-note">Unsaved changes</span>{:else if justSaved}<span class="save-note ok">Saved ✓</span>{/if}
        <button class="btn-primary" onclick={saveConfig}>Save settings</button>
      </div>
    </section>

  {:else if view === 'running'}
    <section class="panel">
      <div class="panel-header">
        <h2>Updating</h2>
        {#if busy}
          <span class="badge badge-running">In progress...</span>
          <button class="btn-sm btn-danger" onclick={() => CancelAll()}>Cancel All</button>
        {:else}
          <span class="badge badge-success">All finished</span>
        {/if}
      </div>

      <ul class="status-list">
        {#each Object.keys(statuses) as name (name)}
          <li class="status-item confirm-item">
            <button class="status-btn" onclick={() => { prevView = 'running'; detailProject = name; view = 'detail'; }}>
              <span class="status-indicator status-{statuses[name]}"></span>
              <span class="status-label">{STATUS_TEXT[statuses[name]] || statuses[name]}</span>
              <span class="name">{name}</span>
            </button>
            {#if statuses[name] === 'running'}
              <button class="btn-sm btn-danger cancel-btn" onclick={(e) => { e.stopPropagation(); CancelProject(name); }}>Cancel</button>
            {:else if finished(name)}
              {#if done(name)}
                <span class="confirm-result">{confirmResults[name]}</span>
                {#if renamed(name)}
                  {#if merges[name]?.state === 'done'}
                    <button class="btn-sm" onclick={() => BrowserOpenURL(merges[name].url)}>Open MR ↗</button>
                  {:else if merges[name]?.state === 'working'}
                    <span class="merge-msg" title={merges[name].message}>{merges[name].message}</span>
                    <button class="btn-sm btn-danger" onclick={() => CancelProject(name)}>Cancel</button>
                  {:else}
                    {#if merges[name]?.state === 'failed'}<button type="button" class="merge-msg merge-failed" class:open={mergeOpen[name]} aria-expanded={!!mergeOpen[name]} title={mergeOpen[name] ? 'Click to fold' : 'Click to show the whole message'} onclick={() => mergeOpen[name] = !mergeOpen[name]}>✗ {merges[name].message}</button>{/if}
                    <button class="btn-sm" onclick={() => openMergeRequest(name)}>{merges[name] ? 'Retry MR' : 'Create MR'}</button>
                  {/if}
                {/if}
                {#if confirmResults[name] !== '…' && merges[name]?.state !== 'working'}
                  <button class="btn-sm" title="Remove from list" onclick={() => dismiss(name)}>✕</button>
                {/if}
              {:else if statuses[name] === 'success'}
                {#if confirmResults[name]}<span class="confirm-result">{confirmResults[name]}</span>{/if}
                <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" bind:value={branchNames[name]} class="branch-name-input" />
                <button class="btn-sm" onclick={() => confirmProject(name)}>Confirm</button>
              {:else}
                {#if confirmResults[name]}<span class="confirm-result">{confirmResults[name]}</span>{/if}
                {#if notStarted[name]}
                  <span class="cleanup-note">not started</span>
                {:else}
                  <span class="cleanup-note">cleanup needed</span>
                  <button class="btn-sm" onclick={() => confirmProject(name)}>Clean up</button>
                {/if}
                <button class="btn-sm" onclick={() => retryProject(name)}>Retry</button>
                {#if notStarted[name]}<button class="btn-sm" title="Remove from list" onclick={() => dismiss(name)}>✕</button>{/if}
              {/if}
            {/if}
          </li>
        {/each}
      </ul>

      {#if Object.keys(statuses).some(n => finished(n) && !done(n)) || Object.keys(statuses).some(n => renamed(n) && !merges[n])}
        <div class="footer-actions">
          {#if Object.keys(statuses).some(n => (statuses[n] === 'failed' || statuses[n] === 'cancelled') && !done(n))}
            <button class="btn-sm btn-danger" onclick={retryFailed}>Retry Failed</button>
          {/if}
          {#if Object.keys(statuses).some(n => renamed(n) && !merges[n])}
            <button class="btn-sm" onclick={() => Object.keys(statuses).filter(n => renamed(n) && !merges[n]).forEach(openMergeRequest)}>Create all MRs</button>
          {/if}
          {#if Object.keys(statuses).some(n => finished(n) && !done(n) && !notStarted[n])}
            <button class="btn-primary" onclick={confirmAll}>Confirm all finished</button>
          {/if}
        </div>
      {/if}
    </section>

  {:else if view === 'detail'}
    <section class="panel">
      <div class="panel-header">
        <button class="btn-sm" onclick={() => view = prevView || 'running'}>← Back</button>
        <h2>{detailProject}</h2>
        <span class="badge badge-{statuses[detailProject]}">{STATUS_TEXT[statuses[detailProject]] || statuses[detailProject]}</span>
      </div>
      {#if results[detailProject]}
        <RunResult status={results[detailProject].status} text={results[detailProject].text} />
        <details class="full-log">
          <summary>Full log</summary>
          <pre class="log-output">{logs[detailProject]}</pre>
        </details>
      {:else}
        <pre class="log-output">{logs[detailProject] || 'Waiting...'}</pre>
      {/if}
      {#if screens[detailProject] && results[detailProject]}
        <details class="full-log">
          <summary>Claude session (last screen)</summary>
          <pre class="log-output live-output">{screens[detailProject]}</pre>
        </details>
      {:else if screens[detailProject]}
        <h3 class="live-title">Claude session {statuses[detailProject] === 'running' ? '(live)' : '(last screen)'}</h3>
        <pre class="log-output live-output">{screens[detailProject]}</pre>
      {/if}
    </section>

  {/if}
</main>

<style>

  main {
    display: flex;
    flex-direction: column;
    height: 100vh;
    padding: 20px 24px;
    gap: 18px;
  }

  /* macOS hidden-inset title bar: drag strip clear of the traffic lights */
  .titlebar {
    position: fixed; top: 0; left: 0; right: 0; height: 28px;
    --wails-draggable: drag;
  }
  main.mac { padding-top: 38px; }

  header {
    user-select: none;
    display: flex;
    flex-wrap: wrap;
    gap: 10px 16px;
    align-items: center;
    justify-content: space-between;
    padding-bottom: 14px;
    border-bottom: 1px solid var(--border-strong);
  }

  h1, h2, h3 {
    font-family: var(--font-head);
    color: var(--text);
  }

  h1 {
    font-size: var(--fs2);
    font-weight: 800;
    letter-spacing: -0.02em;
    display: flex;
    align-items: center;
    gap: 10px;
  }
  /* Dynamate spark: theme text colour (navy on light, cream on dark), no tile */
  .spark { width: 1.6em; height: 1.6em; padding: 0.3em; flex: none; border-radius: 0.35em; color: var(--text); box-sizing: border-box; }

  h2 {
    font-size: var(--fs1);
    font-weight: 700;
    letter-spacing: -0.01em;
  }

  h3 {
    font-size: var(--fs1);
    font-weight: 700;
    margin-top: 20px;
    margin-bottom: 8px;
  }

  nav button.help { width: 1.9em; padding-left: 0; padding-right: 0; border-radius: 50%; }

  nav {
    display: flex;
    gap: 6px;
  }

  nav button {
    background: transparent;
    border: 1px solid transparent;
    color: var(--text-muted);
    padding: 6px 14px;
    border-radius: var(--radius);
    cursor: pointer;
    font: 600 var(--fs0) var(--font-body);
    transition: background 0.15s, color 0.15s;
  }

  nav button:hover {
    background: var(--hover);
    color: var(--text);
  }

  nav button.active {
    background: var(--text);
    border-color: var(--text);
    color: var(--bg);
  }

  .panel {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 14px;
    overflow: hidden;
  }

  /* Settings is long: scroll the whole panel instead of clipping it */
  /* tabs scroll; the Save bar stays pinned to the bottom of the window */
  .config-panel { gap: 0; }
  .config-panel .settings-layout { flex: 1; min-height: 0; overflow-y: auto; padding-bottom: 14px; }
  .config-panel .footer-actions { flex: none; padding-top: 12px; margin-bottom: 0; }
  .settings-section {
    display: grid;
    grid-template-columns: 1fr; /* the tab names the section, head sits above the fields */
    gap: 14px;
    padding: 2px 0 20px; /* the tab frames the section: no card around it */
  }
  .settings-layout { display: grid; grid-template-columns: 180px minmax(0, 1fr); gap: 20px; align-items: start; }
  .settings-tabs { position: sticky; top: 0; display: flex; flex-direction: column; gap: 2px; }
  .settings-tabs button {
    text-align: left; background: none; border: 0; border-radius: 8px; padding: 9px 12px;
    font: 600 var(--fs0) var(--font-body); color: var(--text-muted); cursor: pointer;
    transition: background 0.15s, color 0.15s;
  }
  .settings-tabs button:hover { background: var(--hover); color: var(--text); }
  .settings-tabs button.on { background: var(--selected); color: var(--text); box-shadow: inset 3px 0 0 var(--accent); }
  .settings-section[hidden] { display: none; }
  .replay { align-self: flex-start; }
  .section-head h3 { margin: 0 0 4px; }
  .section-head .hint { margin: 0; }
  .section-body { display: grid; gap: 12px 16px; min-width: 0; }
  .two-col { grid-template-columns: 1fr 1fr; }
  .three-col { grid-template-columns: 1fr 1fr 1fr; }
  .span-2 { grid-column: span 2; }
  .span-3 { grid-column: 1 / -1; }
  .advanced { display: grid; gap: 12px; }
  .advanced summary {
    cursor: pointer;
    font-size: var(--fs0);
    font-weight: 600;
    color: var(--accent);
    width: max-content;
  }
  @media (max-width: 760px) {
    .settings-layout { grid-template-columns: 1fr; }
    .settings-tabs { position: static; flex-direction: row; flex-wrap: wrap; }
    .two-col, .three-col { grid-template-columns: 1fr; }
    .span-2 { grid-column: auto; }
  }
  .config-panel .project-configs {
    overflow: visible;
  }

  .panel-header {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px;
  }
  .panel-header .actions { margin-left: auto; display: flex; gap: 8px; }

  .project-list, .status-list {
    list-style: none;
    flex: 1;
    overflow-y: auto;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
  }

  .project-list ul { list-style: none; margin: 0; padding: 0; }
  .project-list li { display: flex; align-items: center; padding-right: 14px; position: relative; border-top: 1px solid var(--border); transition: background 0.1s; } /* contains the screen-reader-only text, which would otherwise stretch the page */
  .project-list li:hover { background: var(--hover); }
  .project-list li.selected { background: var(--selected); box-shadow: inset 3px 0 0 var(--accent); }

  /* group header: the urgency carries the list, so its colour sits on the rule at the left */
  .group { display: flex; align-items: center; gap: 10px; padding: 0 14px; border-top: 1px solid var(--border); position: sticky; top: 0; z-index: 1; background: var(--surface); }
  .group:first-child { border-top: 0; }
  .group-security { box-shadow: inset 3px 0 0 var(--danger); }
  .group-behind { box-shadow: inset 3px 0 0 var(--warn); }
  .group-current { box-shadow: inset 3px 0 0 var(--success); }
  .group-unknown { box-shadow: inset 3px 0 0 var(--border-strong); }
  .group-toggle { display: flex; align-items: center; gap: 8px; flex: 1; min-width: 0; padding: 10px 0; background: none; border: 0; color: var(--text); font: 700 var(--fs1) var(--font-body); text-align: left; cursor: pointer; }
  .group-toggle .chev { width: 1em; color: var(--text-muted); }
  .group-toggle .count { font-weight: 400; color: var(--text-muted); }
  .group-select { background: none; border: 0; color: var(--accent); font: 600 var(--fs-1) var(--font-body); cursor: pointer; padding: 4px 0; }
  .group-select:hover { text-decoration: underline; }

  /* row: name, then the version it moves to, then what the dashboard says is pending */
  .project-list label {
    display: grid;
    grid-template-columns: 16px minmax(0, 1fr) auto minmax(4.5rem, auto);
    flex: 1;
    min-width: 0;
    align-items: center;
    gap: 12px;
    padding: 9px 14px;
    cursor: pointer;
  }
  .project-list .namecell { display: flex; align-items: center; gap: 8px; min-width: 0; }

  .versions { font: var(--fs-1) var(--font-mono); white-space: nowrap; color: var(--text-muted); }
  .versions .arrow { padding: 0 5px; }
  li .versions .target { font-weight: 600; }
  .group-security + ul .versions .target { color: var(--danger); }
  .group-behind + ul .versions .target { color: var(--warn); }
  .mods { font-size: var(--fs-1); color: var(--danger); white-space: nowrap; text-align: right; }
  .group-behind + ul .mods { color: var(--text-muted); }
  .info { width: 1.5rem; height: 1.5rem; border-radius: 50%; border: 1px solid var(--border-strong); background: none; color: var(--text-muted); font: 700 var(--fs-1) var(--font-body); font-style: italic; cursor: pointer; display: grid; place-items: center; }
  .info:hover, .info[aria-expanded='true'] { color: var(--accent); border-color: var(--accent); }
  .sla-mark { flex: none; font-size: var(--fs-1); font-weight: 700; color: var(--accent); border: 1px solid currentColor; border-radius: 4px; padding: 0 4px; line-height: 1.3; cursor: default; }
  .sla-only { display: inline-flex; align-items: center; gap: 6px; font-size: var(--fs0); font-weight: 600; color: var(--text); cursor: pointer; }
  .sla-only input { accent-color: var(--accent); }
  .save-note { margin-right: 12px; font-size: var(--fs0); color: var(--text-muted); align-self: center; }
  .save-note.err { color: var(--danger); }
  .save-note.ok { color: var(--success); }
  .hint.err { color: var(--danger); }
  .repo-mark { flex: none; font-size: var(--fs0); font-weight: 700; color: var(--accent); cursor: default; }
  .unmatched { list-style: none; margin: 0; padding: 0; display: grid; gap: 6px; }
  .unmatched li { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 18rem); gap: 6px 10px; align-items: center; padding: 8px 10px; border: 1px solid var(--border); border-radius: var(--radius); background: var(--bg); }
  .unmatched .local { font: var(--fs0) var(--font-mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .unmatched select { min-width: 0; width: 100%; }
  .unmatched .sugg { grid-column: 1 / -1; display: flex; flex-wrap: wrap; gap: 6px; }
  .unmatched .sugg:empty { display: none; }
  .unmatched .sugg .btn-sm { white-space: nowrap; }
  .unmatched .why-line { grid-column: 1 / -1; }
  .unmatched .pick { grid-column: 1 / -1; }
  .dash-state { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .dash-ok { color: var(--success); font-weight: 600; font-size: var(--fs0); }
  .core-note { flex: 0 1 auto; min-width: 0; max-width: 22rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .project-list input[type="checkbox"] {
    accent-color: var(--accent);
    width: 16px;
    height: 16px;
  }

  .project-list .name {
    font-weight: 600;
    color: var(--text);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .project-list .badge { flex: none; }

  .status-list li { border-bottom: 1px solid var(--border); }
  .status-list li:last-child { border-bottom: none; }

  .status-item {
    display: flex;
    align-items: center;
  }

  .cancel-btn {
    margin-right: 10px;
    flex-shrink: 0;
  }

  .status-btn {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 14px;
    width: 100%;
    background: transparent;
    border: none;
    color: inherit;
    font: inherit;
    cursor: pointer;
    text-align: left;
    transition: background 0.1s;
  }

  .status-btn:hover { background: var(--hover); }
  .status-btn .name { font-weight: 600; }

  /* Shape + color: filled circle = done/active, square = failed, ring = idle/cancelled */
  .status-indicator {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    flex-shrink: 0;
    border: 2px solid transparent;
  }

  .status-idle { border-color: var(--border-strong); }
  .status-running { background: var(--running-dot); border-color: var(--running); animation: pulse 1.2s ease-in-out infinite; }
  .status-success { background: var(--success); border-color: var(--success); }
  .status-failed { background: var(--danger); border-color: var(--danger); border-radius: 2px; }
  .status-skipped { border-color: var(--success); }
  .status-cancelled { border-color: var(--text-muted); background: linear-gradient(var(--text-muted), var(--text-muted)) center / 100% 2px no-repeat; }

  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.35; }
  }

  @media (prefers-reduced-motion: reduce) {
    .status-running { animation: none; }
    .mode-switch, .mode-switch .knob { transition: none; }
  }
  @media (max-width: 860px) {
    .mode-hint { display: none; }
  }

  .status-label {
    font-size: var(--fs-1);
    font-weight: 700;
    width: 76px;
    color: var(--text-muted);
  }

  .badge {
    font-size: var(--fs-1);
    padding: 2px 8px;
    border: 1px solid currentColor;
    border-radius: var(--radius-sm);
    font-weight: 600;
    white-space: nowrap;
  }

  .badge-success { background: var(--success-bg); color: var(--success); }
  .badge-running { background: var(--running-bg); color: var(--running); }
  .badge-failed { background: var(--danger-bg); color: var(--danger); }
  .badge-cancelled { background: transparent; color: var(--text-muted); border-style: dashed; }
  .badge-idle { background: transparent; color: var(--text-muted); }
  .badge-repo {
    background: transparent;
    color: var(--accent);
    font-weight: 500;
    padding: 1px 6px;
  }

  .footer-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px;
    padding-top: 14px;
    border-top: 1px solid var(--border-strong);
  }

  .update-mode {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: var(--fs0);
    font-weight: 600;
    color: var(--text-muted);
  }
  .mode-label { background: none; border: 0; padding: 0; font: inherit; color: var(--text-muted); cursor: pointer; }
  .mode-label.on { color: var(--text); }
  .mode-switch {
    position: relative; width: 40px; height: 22px; padding: 0; flex: none;
    border-radius: 11px; border: 1px solid var(--border-strong); background: var(--surface); cursor: pointer;
    transition: background 0.15s, border-color 0.15s;
  }
  .mode-switch[aria-checked='true'] { background: var(--accent); border-color: var(--accent); }
  .mode-switch .knob {
    position: absolute; top: 2px; left: 2px; width: 16px; height: 16px; border-radius: 50%;
    background: var(--text); transition: transform 0.15s;
  }
  .mode-switch[aria-checked='true'] .knob { transform: translateX(18px); background: var(--on-accent); }
  .mode-hint { font-weight: 400; font-size: var(--fs-1); }

  input[type="text"], textarea, select {
    background: var(--surface);
    border: 1px solid var(--border-strong);
    color: var(--text);
    padding: 8px 10px;
    border-radius: var(--radius);
    font-family: var(--font-body);
    font-size: var(--fs0);
    transition: border-color 0.15s, box-shadow 0.15s;
  }

  input[type="text"]:focus, textarea:focus, select:focus {
    outline: none;
    border-color: var(--focus);
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--focus) 30%, transparent);
  }

  input::placeholder, textarea::placeholder { color: var(--text-muted); opacity: 0.8; }

  textarea {
    font-family: var(--font-mono);
    font-size: var(--fs-1);
    resize: vertical;
  }

  select { cursor: pointer; }

  .btn-danger { border-color: var(--danger); color: var(--danger); }
  .btn-danger:hover { background: var(--danger-bg); border-color: var(--danger); }

  .project-config-edit label, .edit-field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: var(--fs0);
    font-weight: 600;
    color: var(--text-muted);
  }

  .project-configs {
    flex: 1;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .project-config-item {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 10px 14px;
    background: var(--surface);
  }

  .project-config-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .project-config-header div {
    display: flex;
    gap: 4px;
  }

  .project-config-edit {
    margin-top: 10px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .add-project {
    display: flex;
    gap: 8px;
    align-items: center;
  }

  .full-log + .full-log { margin-top: 10px; }
  .full-log summary { cursor: pointer; color: var(--text-muted); font-size: var(--fs0); margin-bottom: 6px; }
  .live-title { margin: 12px 0 6px; font-size: var(--fs0); color: var(--text-muted); }
  .live-output { font-size: var(--fs-1); white-space: pre; overflow-x: auto; }

  .log-output {
    flex: 1;
    overflow-y: auto;
    background: var(--terminal-bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    padding: 14px;
    font-family: var(--font-mono);
    font-size: var(--fs-1);
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--terminal-text);
  }

  .empty {
    text-align: center;
    color: var(--text-muted);
    padding: 40px;
    font-size: var(--fs0);
  }

  .update-banner, .version {
    margin-left: auto;
    margin-right: 16px;
  }
  .update-banner {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 4px 4px 12px;
    border: 1px solid var(--accent);
    border-radius: 6px;
    background: var(--running-bg);
    color: var(--text);
    font-size: var(--fs0);
  }
  .version {
    font-family: var(--font-mono);
    font-size: var(--fs-1);
    color: var(--text-muted);
  }

  .search-box {
    flex: 0 1 250px;
    min-width: 9rem; /* the filter keeps its room; the offline note gives way */
  }

  .search-input {
    width: 100%;
    padding: 6px 10px;
    font-size: var(--fs0);
  }

  .confirm-item {
    padding: 10px 14px;
    gap: 10px;
    flex-wrap: wrap;
  }

  .branch-name-input {
    flex: 1;
    max-width: 300px;
    font-family: var(--font-mono);
    font-size: var(--fs-1);
  }

  .cleanup-note {
    font-size: var(--fs0);
    color: var(--text-muted);
    font-style: italic;
  }

  .confirm-result {
    font-family: var(--font-mono);
    font-size: var(--fs-1);
    color: var(--text-muted);
  }

  .merge-msg {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: var(--fs-1);
    color: var(--text-muted);
  }

  .merge-failed { color: var(--danger); background: none; border: none; padding: 0; font: inherit; font-size: var(--fs-1); text-align: left; cursor: pointer; }
  .merge-failed.open { white-space: pre-wrap; overflow-wrap: anywhere; flex-basis: 100%; order: 10; user-select: text; }
</style>
