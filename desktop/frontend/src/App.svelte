<script>
  const isMac = navigator.userAgent.includes('Macintosh');
  import { onMount, tick } from 'svelte';
  import { DiscoverProjects, CoreStatuses, RefreshCore, RefreshReleases, GetConfig, SaveConfig, StartUpdate, CancelProject, CancelAll, RenameBranch, CleanupBranch, RetryProjects, GetClaudeDefaults, SetUpdateMode, FinalBranch, SaveProjectFile, MergeRequest, GetVersion, GetUpdate, InstallUpdate } from '../wailsjs/go/main/App';
  import { EventsOn, BrowserOpenURL } from '../wailsjs/runtime/runtime';

  const FONTS = {
    atkinson: { label: 'Atkinson Hyperlegible', scale: 1.125, css: "'Atkinson Hyperlegible Next Variable', 'Atkinson Hyperlegible Next'" },
    jetbrains: { label: 'JetBrains Mono', scale: 1, css: "'JetBrains Mono Variable', 'JetBrains Mono'" },
    spacegrotesk: { label: 'Space Grotesk', scale: 1.1, css: "'Space Grotesk Variable', 'Space Grotesk'" },
  };
  const load = (k, ok, d) => { try { const v = localStorage.getItem(k); return ok(v) ? v : d; } catch { return d; } };
  let theme = $state(load('theme', v => ['system', 'light', 'dark'].includes(v), 'system'));
  let fontSize = $state(load('fontSize', v => ['14', '16', '18', '20'].includes(v), '16'));
  const dark = window.matchMedia('(prefers-color-scheme: dark)');
  let systemDark = $state(dark.matches);
  dark.addEventListener('change', e => systemDark = e.matches);
  $effect(() => {
    document.documentElement.dataset.theme = theme === 'system' ? (systemDark ? 'dark' : 'light') : theme;
    // per-font scale evens out the fonts' different x-heights
    document.documentElement.style.fontSize = fontSize * FONTS[font].scale + 'px';
    try { localStorage.setItem('theme', theme); localStorage.setItem('fontSize', fontSize); } catch {}
  });
  let font = $state((() => { try { return localStorage.getItem('font'); } catch { return null; } })());
  if (!Object.hasOwn(FONTS, font)) font = 'atkinson';
  $effect(() => {
    const stack = FONTS[font].css + ', system-ui, sans-serif';
    document.documentElement.style.setProperty('--font-head', stack);
    document.documentElement.style.setProperty('--font-body', stack);
    try { localStorage.setItem('font', font); } catch {}
  });

  // First-run tour: coach marks over real elements, replayable from the (?) button / Settings.
  const STEPS = [
    { view: 'projects', sel: '[data-tour=projects]', title: 'Your projects', text: 'Drupal projects found in your base folders show up here. Tick the ones you want to update, or use the filter and Toggle All above.' },
    { view: 'projects', sel: '[data-tour=mode]', title: 'Update mode', text: 'Standard applies all minor and patch updates. Disaster only applies security fixes.' },
    { view: 'projects', sel: '[data-tour=start]', title: 'Run the update', text: 'Claude updates each selected project on its own temporary branch, several at a time. When a project finishes you can review it, confirm, then merge it.' },
    { view: 'config', tab: 'projects', sel: '[data-tour=folders]', title: 'Where projects come from', text: 'Add the folders to scan here. Nothing shows up on the Projects tab until you do.' },
    { view: 'config', tab: 'claude', sel: '[data-tour=claude]', openAdvanced: true, title: 'Claude settings', text: 'Model, permission mode and timeout, plus the allowed tools and the prompt in the advanced part (opened here). Per-project overrides are further down.' },
    { view: 'projects', sel: '[data-tour=help]', title: 'That is it', text: 'Click (?) any time to see this tour again.' },
  ];
  function setMode(m) { config.UpdateMode = m; SetUpdateMode(m); }
  let tour = $state(-1);
  let tourRect = $state(null);
  async function showStep(i) {
    if (i >= STEPS.length) return endTour();
    tour = i;
    view = STEPS[i].view;
    if (STEPS[i].tab) settingsTab = STEPS[i].tab;
    await tick();
    const adv = document.querySelector('details.advanced');
    if (adv) adv.open = !!STEPS[i].openAdvanced;
    await tick();
    const el = document.querySelector(STEPS[i].sel);
    // a target taller than most of the window is aligned to the top so its start is visible
    el?.scrollIntoView({ block: el.offsetHeight > innerHeight * 0.6 ? 'start' : 'nearest' });
    const r = el?.getBoundingClientRect();
    tourRect = r ? { top: r.top, left: r.left, width: r.width, height: r.height } : null;
  }
  function endTour() {
    tour = -1; tourRect = null; view = 'projects';
    try { localStorage.setItem('tourDone', '1'); } catch {}
  }
  // Whole-pixel positions only: translate(-50%) lands on half pixels and blurs the text.
  function tourStyle(r) {
    const w = 320, h = 200, mid = Math.round((innerWidth - w) / 2);
    if (!r) return `left:${mid}px;bottom:24px`;
    const left = Math.round(Math.max(16, Math.min(r.left, innerWidth - w - 16)));
    if (innerHeight - (r.top + r.height) >= h) return `left:${left}px;top:${Math.round(r.top + r.height + 12)}px`;
    if (r.top >= h) return `left:${left}px;bottom:${Math.round(innerHeight - r.top + 12)}px`;
    return `left:${mid}px;bottom:24px`;
  }
  // First run: appearance picker first (changes apply live behind it), then the tour.
  let welcome = $state(false);
  function finishWelcome() {
    welcome = false;
    try { localStorage.setItem('setupDone', '1'); } catch {}
    let toured = false;
    try { toured = !!localStorage.getItem('tourDone'); } catch {}
    if (!toured) setTimeout(() => showStep(0), 300);
  }
  onMount(() => {
    let setup = false, toured = false;
    try { setup = !!localStorage.getItem('setupDone'); toured = !!localStorage.getItem('tourDone'); } catch {}
    if (!setup && !toured) welcome = true; // existing users (tour already seen) are not asked
    else if (!toured) setTimeout(() => showStep(0), 600);
  });

  const TABS = [['projects', 'Projects'], ['branches', 'Branch defaults'], ['claude', 'Claude'], ['overrides', 'Project overrides'], ['appearance', 'Appearance']];
  let settingsTab = $state('projects');
  const THEMES = [['system', 'System'], ['light', 'Light'], ['dark', 'Dark']];
  const SIZES = [['14', 'Small'], ['16', 'Default'], ['18', 'Large'], ['20', 'Extra large']];
  const FONT_ITEMS = Object.entries(FONTS).map(([k, f]) => [k, f.label, f.css]);

  let view = $state('projects');
  let projects = $state([]);
  let selected = $state({});
  let claudeDefaults = $state({ prompt: '', allowedTools: [] });
  let config = $state({ BaseFolders: [], ProjectPaths: [], MaxConcurrent: 3, ExcludedProjects: [], Projects: {} });

  let statuses = $state({});
  let logs = $state({});
  let screens = $state({}); // live Claude terminal per project
  let notStarted = $state({}); // projects that failed before running (discovery), so confirm must not clean up their branch
  let detailProject = $state('');

  let editingProject = $state(null);
  let newProjectName = $state('');

  let isRunning = $state(false);

  let searchFilter = $state('');
  let statusFilter = $state('all');
  // own keys only: project.core comes from composer.lock, so "constructor" must not hit Object.prototype
  const coreOf = (p) => (p.core && Object.hasOwn(coreStatus, p.core) ? coreStatus[p.core] : null);
  const STATUS_FILTERS = [['all', 'All'], ['current', 'Up to date'], ['behind', 'Behind'], ['security', 'Security']];
  // projects passing both the text and the update-status filter (unknown status only shows under All)
  const visible = $derived(projects.filter(p =>
    (!searchFilter || p.name.toLowerCase().includes(searchFilter.toLowerCase())) &&
    (statusFilter === 'all' || coreOf(p)?.status === statusFilter)));

  let branchNames = $state({});
  let confirmResults = $state({});
  let prevView = $state('');
  let merges = $state({}); // project -> {state: working|done|failed, message, url}
  let repoSaved = $state({}); // project -> result of writing drupal-updater.yaml
  let version = $state('');
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
      if (status === 'success' && !branchNames[name]) {
        FinalBranch(name).then(b => { if (!branchNames[name]) branchNames[name] = b; }).catch(() => {});
      }
      const cmd = update.command || update.Command;
      if (cmd === 'discover') notStarted[name] = true; // never ran: no temp branch of this run to clean up
      const out = update.output || update.Output;
      if (out) {
        logs[name] += cmd + '\n' + out + '\n';
      } else if (cmd) {
        logs[name] += '$ ' + cmd + '\n';
      }
    });

    EventsOn('merge-update', (u) => { merges[u.project] = u; });
    EventsOn('update-available', (r) => { update = r; });
    version = await GetVersion();
    update = (await GetUpdate()) || update;

    const loaded = await GetConfig();
    claudeDefaults = await GetClaudeDefaults();
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
    };
    await loadProjects();
  });

  let coreStatus = $state({});
  const CORE_TITLE = {
    current: () => 'Drupal core is up to date',
    behind: (r) => `Drupal core is behind (${r.latest} available), no security update`,
    security: (r) => `Security update needed (${r.latest} available)`,
  };
  async function loadProjects() {
    try {
      projects = await DiscoverProjects();
      projects.forEach(p => {
        if (!(p.name in selected)) selected[p.name] = false;
      });
    } catch (e) {
      projects = [];
    }
    await updateStatuses();
    refreshCores(false); // switches to the version committed on origin, fetching only when the last fetch is over a day old
  }
  // dots are optional: offline or a failed lookup just leaves them out
  async function updateStatuses() {
    try { coreStatus = (await CoreStatuses([...new Set(projects.map(p => p.core).filter(Boolean))])) || {}; } catch {}
  }
  let refreshing = $state(false);
  let refreshDone = $state(0);
  // Re-reads every project's core version from origin's start branch, 4 projects at a time.
  // force = the Refresh button: fetch everything now and download Drupal's release list again.
  async function refreshCores(force) {
    if (refreshing || projects.length === 0) return;
    refreshing = true; refreshDone = 0;
    try {
      if (force) await RefreshReleases();
      const queue = [...projects];
      const worker = async () => {
        for (let p; (p = queue.shift()); refreshDone++) {
          try { const v = await RefreshCore(p.name, force); if (v) p.core = v; } catch {}
        }
      };
      await Promise.all([worker(), worker(), worker(), worker()]);
      await updateStatuses();
    } finally { refreshing = false; }
  }

  // acts on what the filters show, so hidden projects are never selected by accident
  function toggleAll() {
    const allSelected = visible.every(p => selected[p.name]);
    visible.forEach(p => selected[p.name] = !allSelected);
  }

  function startUpdate() {
    doStart();
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

  function showDetail(name) {
    prevView = view;
    detailProject = name;
    view = 'detail';
  }

  async function saveConfig() {
    // Store defaults as empty so future built-in improvements still apply.
    const toSave = { ...config };
    if (toSave.ClaudePrompt === claudeDefaults.prompt) toSave.ClaudePrompt = '';
    if (JSON.stringify(toSave.ClaudeAllowedTools) === JSON.stringify(claudeDefaults.allowedTools)) toSave.ClaudeAllowedTools = [];
    await SaveConfig(toSave);
    await loadProjects();
    view = 'projects';
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
    config.Projects = config.Projects;
    if (editingProject === name) editingProject = null;
  }

  const finished = n => ['success', 'failed', 'cancelled'].includes(statuses[n]);
  const done = n => !!confirmResults[n] && !confirmResults[n].startsWith('✗'); // '…' = in flight
  const busy = $derived(Object.values(statuses).some(s => s === 'running' || s === 'idle'));

  // Clear a row so it can be (re)started.
  function resetRow(name) {
    statuses[name] = 'idle';
    logs[name] = '';
    for (const o of [screens, confirmResults, merges, branchNames, notStarted]) delete o[name];
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
    for (const o of [statuses, logs, screens, confirmResults, merges, branchNames, notStarted]) delete o[name];
    if (Object.keys(statuses).length === 0) {
      isRunning = false;
      view = 'projects';
    }
  }
</script>

<svelte:window
  onresize={() => tour >= 0 && showStep(tour)}
  onkeydown={(e) => { if (tour < 0) return; if (e.key === 'Escape') endTour(); else if (e.key === 'ArrowRight' || e.key === 'Enter') showStep(tour + 1); else if (e.key === 'ArrowLeft' && tour > 0) showStep(tour - 1); }} />

{#snippet seg(label, items, current, pick)}
  <div class="field">
    <span>{label}</span>
    <div class="seg" role="radiogroup" aria-label={label}>
      {#each items as [value, text, face]}
        <button type="button" role="radio" aria-checked={current === value} class:on={current === value} style:font-family={face} onclick={() => pick(value)}>{text}</button>
      {/each}
    </div>
  </div>
{/snippet}

{#if welcome}
  <div class="tour-dim"></div>
  <div class="welcome-wrap"><div class="tour-card welcome" role="dialog" aria-label="Welcome">
    <h3>Welcome to Drupal Updater</h3>
    <p>First, make it comfortable. Changes apply right away and you can change them later in Settings.</p>
    <div class="welcome-fields">
      {@render seg('Theme', THEMES, theme, v => theme = v)}
      {@render seg('Font', FONT_ITEMS, font, v => font = v)}
      {@render seg('Font size', SIZES, fontSize, v => fontSize = v)}
    </div>
    <div class="tour-actions">
      <span class="spacer"></span>
      <button class="btn-primary" onclick={finishWelcome}>Continue</button>
    </div>
  </div></div>
{/if}

{#if tour >= 0}
  <div class="tour-block"></div>
  {#if tourRect}
    <div class="tour-spot" style="top:{tourRect.top - 6}px;left:{tourRect.left - 6}px;width:{tourRect.width + 12}px;height:{tourRect.height + 12}px"></div>
  {:else}
    <div class="tour-dim"></div>
  {/if}
  <div class="tour-card" role="dialog" aria-label="Tutorial" style={tourStyle(tourRect)}>
    <div class="tour-count">{tour + 1} / {STEPS.length}</div>
    <h3>{STEPS[tour].title}</h3>
    <p>{STEPS[tour].text}</p>
    <div class="tour-actions">
      <button class="btn-sm" onclick={endTour}>Skip</button>
      <span class="spacer"></span>
      {#if tour > 0}<button class="btn-sm" onclick={() => showStep(tour - 1)}>Back</button>{/if}
      <button class="btn-primary" onclick={() => showStep(tour + 1)}>{tour === STEPS.length - 1 ? 'Done' : 'Next'}</button>
    </div>
  </div>
{/if}

<main class:mac={isMac}>
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
      <button data-tour="help" class="help" title="Show the tutorial" aria-label="Show the tutorial" disabled={busy} onclick={() => showStep(0)}>?</button>
      {#if isRunning}
        <button class:active={view === 'running'} onclick={() => view = 'running'}>
          {busy ? '● Running' : '⚑ Review'}
        </button>
      {/if}
    </nav>
  </header>

  {#if view === 'projects'}
    <section class="panel">
      <div class="panel-header">
        <h2>Select projects to update</h2>
        <div class="search-box">
          <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" bind:value={searchFilter} placeholder="Filter projects..." class="search-input" />
        </div>
        <div class="seg seg-sm" role="radiogroup" aria-label="Filter by update status">
          {#each STATUS_FILTERS as [key, label]}
            <button type="button" role="radio" aria-checked={statusFilter === key} class:on={statusFilter === key} onclick={() => statusFilter = key}>{#if key !== 'all'}<i class="dot dot-{key}"></i>{/if}{label}</button>
          {/each}
        </div>
        <div class="actions">
          <button class="btn-sm" onclick={() => refreshCores(true)} disabled={refreshing} title="Fetch every project's origin and check Drupal's latest releases again">{refreshing ? `Refreshing ${refreshDone}/${projects.length}` : 'Refresh'}</button>
          <button class="btn-sm" onclick={toggleAll}>Toggle All</button>
        </div>
      </div>

      <ul class="project-list" data-tour="projects">
        {#each visible as project}
          <li class:selected={selected[project.name]}>
            <label>
              <input type="checkbox" bind:checked={selected[project.name]} />
              <span class="namecell">
                <span class="name">{project.name}</span>
                {#if project.repoFile}<span class="badge badge-repo" title="Settings from the repo's drupal-updater.yaml">repo config</span>{/if}
              </span>
              <span class="corecell">
                {#if project.core}{@const st = coreOf(project)}<span class="core" title={(st && CORE_TITLE[st.status]?.(st)) || 'Installed Drupal core (composer.lock)'}>{#if st}<i class="dot dot-{st.status}"></i>{/if}{project.core}</span>{/if}
              </span>
              <!-- rtl + ellipsis cuts the front of the path; <bdi> keeps the text itself left-to-right -->
              <span class="path" title={project.path}><bdi>{project.path}</bdi></span>
            </label>
          </li>
        {/each}
      </ul>

      {#if projects.length > 0 && visible.length === 0}
        <p class="empty">No projects match the filters.</p>
      {/if}

      {#if projects.length === 0}
        <p class="empty">No projects found. Check your project folders in Settings.</p>
      {/if}

      <div class="footer-actions">
        <div class="update-mode" data-tour="mode">
          <span>Mode</span>
          <button type="button" class="mode-label" class:on={config.UpdateMode !== 'disaster'} onclick={() => setMode('standard')}>Standard</button>
          <button type="button" role="switch" class="mode-switch" aria-label="Disaster mode (security fixes only)" aria-checked={config.UpdateMode === 'disaster'} onclick={() => setMode(config.UpdateMode === 'disaster' ? 'standard' : 'disaster')}><span class="knob"></span></button>
          <button type="button" class="mode-label" class:on={config.UpdateMode === 'disaster'} onclick={() => setMode('disaster')}>Disaster</button>
          <span class="mode-hint">{config.UpdateMode === 'disaster' ? 'security fixes only' : 'all minor and patch updates'}</span>
        </div>
        <button class="btn-primary" data-tour="start" onclick={startUpdate} disabled={!projects.some(p => selected[p.name])}>
          Update Selected
        </button>
      </div>
    </section>

  {:else if view === 'config'}
    <section class="panel config-panel">
      <div class="settings-layout">
        <nav class="settings-tabs" role="tablist" aria-orientation="vertical">
          {#each TABS as [key, label]}
            <button type="button" role="tab" aria-selected={settingsTab === key} class:on={settingsTab === key} onclick={() => settingsTab = key}>{label}</button>
          {/each}
        </nav>
        <div class="settings-content">
      <div class="settings-section" hidden={settingsTab !== 'appearance'}>
        <div class="section-head">
          <h3>Appearance</h3>
          <p class="hint">Applies immediately, stored on this machine.</p>
        </div>
        <div class="section-body">
          {@render seg('Theme', THEMES, theme, v => theme = v)}
          {@render seg('Font', FONT_ITEMS, font, v => font = v)}
          {@render seg('Font size', SIZES, fontSize, v => fontSize = v)}
          <div class="field">
            <span>Tutorial</span>
            <button class="btn-sm replay" onclick={() => showStep(0)}>Show tutorial again</button>
          </div>
        </div>
      </div>

      <div class="settings-section" hidden={settingsTab !== 'projects'} data-tour="folders">
        <div class="section-head">
          <h3>Projects</h3>
          <p class="hint">Where Drupal projects are found.</p>
        </div>
        <div class="section-body two-col">
          <label class="field">
            Base folders <span class="hint">one per line, scanned one level deep</span>
            <textarea autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" rows="3" value={config.BaseFolders?.join('\n') || ''}
              onchange={(e) => config.BaseFolders = e.target.value.split('\n').map(s => s.trim()).filter(Boolean)}></textarea>
          </label>
          <label class="field">
            Extra project paths <span class="hint">one per line, single projects</span>
            <textarea autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" rows="3" value={config.ProjectPaths?.join('\n') || ''}
              onchange={(e) => config.ProjectPaths = e.target.value.split('\n').map(s => s.trim()).filter(Boolean)}></textarea>
          </label>
          <label class="field span-2">
            Excluded projects <span class="hint">comma-separated, * wildcards</span>
            <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" value={config.ExcludedProjects?.join(', ') || ''}
              onchange={(e) => config.ExcludedProjects = e.target.value.split(',').map(s => s.trim()).filter(Boolean)} />
          </label>
          <label class="field narrow">
            Scan depth <span class="hint">levels below a base folder</span>
            <input type="number" bind:value={config.ScanDepth} min="1" max="10" />
          </label>
          <label class="field narrow">
            Max concurrent runs
            <input type="number" bind:value={config.MaxConcurrent} min="1" max="20" />
          </label>
        </div>
      </div>

      <div class="settings-section" hidden={settingsTab !== 'branches'}>
        <div class="section-head">
          <h3>Branch defaults</h3>
          <p class="hint">For every project. Project overrides and a repo's <code>drupal-updater.yaml</code> win.</p>
        </div>
        <div class="section-body two-col">
          <label class="field">
            Start branch
            <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" placeholder="master|main" bind:value={config.Defaults.GitStartBranch} />
          </label>
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

      <div class="settings-section" hidden={settingsTab !== 'claude'} data-tour="claude">
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
          <label class="field">
            Timeout (minutes)
            <input type="number" bind:value={config.ClaudeTimeoutMinutes} min="1" />
          </label>
          <details class="advanced span-3">
            <summary>Advanced: allowed tools and prompt</summary>
            <label class="field">
              Allowed tools <span class="hint">one per line</span>
              <textarea autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" rows="5" value={config.ClaudeAllowedTools?.join('\n') || ''}
                onchange={(e) => config.ClaudeAllowedTools = e.target.value.split('\n').map(s => s.trim()).filter(Boolean)}></textarea>
            </label>
            <label class="field">
              Prompt
              <span class="hint">Placeholders: {'{{project}} {{path}} {{start_branch}} {{temp_branch}} {{ignored_packages}} {{smoke_paths}} {{project_instructions}} {{updates}} {{result_file}}'}</span>
              <textarea autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" rows="14" bind:value={config.ClaudePrompt}></textarea>
            </label>
            <div><button class="btn-sm" onclick={() => config.ClaudePrompt = claudeDefaults.prompt}>Reset prompt to default</button></div>
          </details>
        </div>
      </div>

      <div class="settings-section" hidden={settingsTab !== 'overrides'}>
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
                    <label>
                      Start branch
                      <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" placeholder={config.Defaults.GitStartBranch || 'master'} bind:value={config.Projects[name].GitStartBranch} />
                    </label>
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
                    <label>
                      Ignored packages (comma-separated)
                      <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" value={config.Projects[name].IgnoredPackages?.join(', ') || ''}
                        onchange={(e) => config.Projects[name].IgnoredPackages = e.target.value.split(',').map(s => s.trim()).filter(Boolean)} />
                    </label>
                    <label>
                      Extra smoke-test paths (comma-separated; prefix admin: for logged-in)
                      <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" value={config.Projects[name].SmokePaths?.join(', ') || ''}
                        onchange={(e) => config.Projects[name].SmokePaths = e.target.value.split(',').map(s => s.trim()).filter(Boolean)} />
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
        {#each Object.keys(statuses) as name}
          <li class="status-item confirm-item">
            <button class="status-btn" onclick={() => { prevView = 'running'; detailProject = name; view = 'detail'; }}>
              <span class="status-indicator status-{statuses[name]}"></span>
              <span class="status-label">{statuses[name].toUpperCase()}</span>
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
                    {#if merges[name]?.state === 'failed'}<span class="merge-msg merge-failed" title={merges[name].message}>✗ {merges[name].message}</span>{/if}
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
        <span class="badge badge-{statuses[detailProject]}">{statuses[detailProject]?.toUpperCase()}</span>
      </div>
      <pre class="log-output">{logs[detailProject] || 'Waiting...'}</pre>
      {#if screens[detailProject]}
        <h3 class="live-title">Claude session {statuses[detailProject] === 'running' ? '(live)' : '(last screen)'}</h3>
        <pre class="log-output live-output">{screens[detailProject]}</pre>
      {/if}
    </section>

  {/if}
</main>

<style>
  /* Dynamate brand tokens. Light is default, dark follows the OS. */
  :global(:root) {
    --navy: #03102b;
    --beige: #ede9e3;
    --cream: #fffbf5;
    --warm: #f5f1e9;
    --warm-strong: #e3ddd3;
    --grey: #d8d9d8;
    --blue: #06428f;
    --red: #f1062c;
    --sky: #78caf0;

    --bg: var(--beige);
    --surface: var(--cream);
    --text: var(--navy);
    --text-muted: #4b566c;
    --border: var(--grey);
    --border-strong: #b9bcc2;
    --hover: var(--warm);
    --selected: var(--warm-strong);
    --accent: var(--blue);
    --accent-hover: #04336f;
    --on-accent: var(--cream);
    --danger: #c70522;
    --danger-bg: #fde8eb;
    --success: #14703f;
    --warn: #c76a00;
    --success-bg: #dff1e7;
    --running: var(--blue);
    --running-bg: #dcf0fb;
    --running-dot: var(--sky);
    --terminal-bg: var(--navy);
    --terminal-text: #e8e4dc;
    --focus: var(--blue);

    --font-head: 'JetBrains Mono Variable', 'JetBrains Mono', ui-monospace, monospace;
    --font-body: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
    --font-mono: ui-monospace, 'SF Mono', Menlo, monospace;
    --radius: 6px;
    --radius-sm: 4px;
    color-scheme: light;
  }

  /* data-theme is always set from script (system preference resolved there) */
  :global(:root[data-theme='dark']) {
    --bg: var(--navy);
    --surface: #07173a;
    --text: var(--cream);
    --text-muted: #a3adc2;
    --border: #1c2c52;
    --border-strong: #2f4274;
    --hover: #0c1f48;
    --selected: #12295a;
    --accent: var(--sky);
    --accent-hover: #9ad6f5;
    --on-accent: var(--navy);
    --danger: #ff5a73;
    --danger-bg: #3a0f22;
    --success: #5fd39a;
    --warn: #ffb454;
    --success-bg: #0d2e2a;
    --running: var(--sky);
    --running-bg: #0b2a4d;
    --running-dot: var(--sky);
    --terminal-bg: #020b1f;
    --terminal-text: #e3ddd3;
    --focus: var(--sky);
    color-scheme: dark;
  }

  :global(*) {
    margin: 0;
    padding: 0;
    box-sizing: border-box;
  }

  :global(body) {
    font-family: var(--font-body);
    background: var(--bg);
    color: var(--text);
    overflow: hidden;
    height: 100vh;
    -webkit-font-smoothing: antialiased;
  }

  :global(:focus-visible) {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }

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
    font-size: 1.35rem;
    font-weight: 800;
    letter-spacing: -0.02em;
  }

  /* the spark: the leading diamond is the one red accent */
  h1 { display: flex; align-items: center; gap: 10px; }
  /* Dynamate spark: theme text colour (navy on light, cream on dark), no tile */
  .spark { width: 1.6em; height: 1.6em; padding: 0.3em; flex: none; border-radius: 0.35em; color: var(--text); box-sizing: border-box; }

  h2 {
    font-size: 1.1rem;
    font-weight: 700;
    letter-spacing: -0.01em;
  }

  h3 {
    font-size: 0.95rem;
    font-weight: 700;
    margin-top: 20px;
    margin-bottom: 8px;
  }

  nav button.help { width: 1.9em; padding-left: 0; padding-right: 0; border-radius: 50%; }
  .tour-block { position: fixed; inset: 0; z-index: 100; }
  .tour-dim { position: fixed; inset: 0; z-index: 101; background: rgba(3, 16, 43, 0.6); }
  .tour-spot {
    position: fixed; z-index: 101; border-radius: 10px; pointer-events: none;
    box-shadow: 0 0 0 9999px rgba(3, 16, 43, 0.6), 0 0 0 2px var(--accent);
    transition: all 0.2s ease;
  }
  .tour-card {
    position: fixed; z-index: 102; width: 320px; padding: 16px;
    background: var(--surface); color: var(--text);
    border: 1px solid var(--border-strong); border-radius: var(--radius);
    box-shadow: 0 12px 32px rgba(0, 0, 0, 0.35);
  }
  .welcome-wrap { position: fixed; inset: 0; z-index: 102; display: grid; place-items: center; pointer-events: none; }
  .tour-card.welcome { position: static; width: 540px; pointer-events: auto; }
  .welcome-fields { display: grid; gap: 14px; margin-top: 14px; }
  .welcome-fields .seg { flex-wrap: wrap; }
  .tour-card h3 { margin: 4px 0 6px; }
  .tour-card p { font-size: 0.9rem; line-height: 1.45; }
  .tour-count { font-size: 0.72rem; color: var(--text-muted); }
  .tour-actions { display: flex; gap: 8px; margin-top: 14px; align-items: center; }
  .tour-actions .spacer { flex: 1; }

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
    font: 600 0.82rem var(--font-body);
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
  .config-panel .footer-actions { flex: none; }
  .settings-section {
    display: grid;
    grid-template-columns: 1fr; /* the tab names the section, head sits above the fields */
    gap: 14px;
    padding: 20px;
    margin-bottom: 14px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
  }
  .settings-layout { display: grid; grid-template-columns: 180px minmax(0, 1fr); gap: 20px; align-items: start; }
  .settings-tabs { position: sticky; top: 0; display: flex; flex-direction: column; gap: 2px; }
  .settings-tabs button {
    text-align: left; background: none; border: 0; border-radius: 8px; padding: 9px 12px;
    font: 600 0.88rem var(--font-body); color: var(--text-muted); cursor: pointer;
    transition: background 0.15s, color 0.15s;
  }
  .settings-tabs button:hover { background: var(--hover); color: var(--text); }
  .settings-tabs button.on { background: var(--selected); color: var(--text); box-shadow: inset 3px 0 0 var(--accent); }
  .settings-section[hidden] { display: none; }
  .seg {
    display: inline-flex; align-self: flex-start; padding: 3px; gap: 2px;
    background: var(--bg); border: 1px solid var(--border-strong); border-radius: 10px;
  }
  .seg button {
    background: none; border: 0; border-radius: 7px; padding: 6px 14px;
    font: 600 0.85rem var(--font-body); color: var(--text-muted); cursor: pointer;
    transition: background 0.15s, color 0.15s;
  }
  .seg-sm button { display: inline-flex; align-items: center; gap: 6px; padding: 4px 10px; font-size: 0.78rem; }
  .seg button:hover { color: var(--text); }
  .seg button.on { background: var(--accent); color: var(--on-accent); }
  .replay { align-self: flex-start; }
  .section-head h3 { margin: 0 0 4px; }
  .section-head .hint { margin: 0; }
  .section-body { display: grid; gap: 12px 16px; min-width: 0; }
  .two-col { grid-template-columns: 1fr 1fr; }
  .three-col { grid-template-columns: 1fr 1fr 1fr; }
  .span-2 { grid-column: span 2; }
  .span-3 { grid-column: 1 / -1; }
  .field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--text-muted);
    min-width: 0;
  }
  .field .hint { font-weight: 400; }
  .field.narrow { max-width: 160px; }
  .advanced { display: flex; flex-direction: column; gap: 12px; }
  .advanced[open] { display: grid; gap: 12px; }
  .advanced summary {
    cursor: pointer;
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--accent);
    width: max-content;
  }
  @media (max-width: 760px) {
    .settings-section { grid-template-columns: 1fr; gap: 10px; }
    .settings-layout { grid-template-columns: 1fr; }
    .settings-tabs { position: static; flex-direction: row; flex-wrap: wrap; }
    .two-col, .three-col { grid-template-columns: 1fr; }
    .span-2 { grid-column: auto; }
  }
  .config-panel .project-configs {
    overflow: visible;
  }
  .config-panel .footer-actions { padding-top: 12px; margin-bottom: 0; }

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

  .project-list li {
    border-bottom: 1px solid var(--border);
    transition: background 0.1s;
  }

  .project-list li:last-child { border-bottom: none; }

  .project-list li:hover { background: var(--hover); }

  .project-list li.selected {
    background: var(--selected);
    box-shadow: inset 3px 0 0 var(--accent);
  }

  /* fixed columns so version and path line up on every row */
  .project-list label {
    display: grid;
    grid-template-columns: 16px clamp(8rem, 30%, 16rem) 6.5rem minmax(0, 1fr);
    align-items: center;
    gap: 12px;
    padding: 10px 14px;
    cursor: pointer;
  }
  .project-list .namecell { display: flex; align-items: center; gap: 8px; min-width: 0; }

  .project-list .core { display: inline-flex; align-items: center; gap: 6px; font: 0.78rem var(--font-mono); color: var(--text-muted); opacity: 0.8; }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--text-muted); flex: none; }
  .dot-current { background: var(--success); }
  .dot-behind { background: var(--warn); }
  .dot-security { background: var(--danger); }

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

  .project-list .path {
    font-family: var(--font-mono);
    font-size: 0.72rem;
    color: var(--text-muted);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    direction: rtl;
    text-align: right;
  }

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
  .status-cancelled { border-color: var(--text-muted); background: linear-gradient(var(--text-muted), var(--text-muted)) center / 100% 2px no-repeat; }

  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.35; }
  }

  @media (prefers-reduced-motion: reduce) {
    .status-running { animation: none; }
  }

  .status-label {
    font-size: 0.7rem;
    font-weight: 700;
    letter-spacing: 0.04em;
    width: 76px;
    color: var(--text-muted);
  }

  .badge {
    font-size: 0.72rem;
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
    align-items: center;
    gap: 12px;
    padding-top: 14px;
    border-top: 1px solid var(--border-strong);
  }

  .update-mode {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 0.82rem;
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
  .mode-hint { font-weight: 400; font-size: 0.78rem; }

  input[type="text"], input[type="number"], textarea, select {
    background: var(--surface);
    border: 1px solid var(--border-strong);
    color: var(--text);
    padding: 8px 10px;
    border-radius: var(--radius);
    font-family: var(--font-body);
    font-size: 0.85rem;
    transition: border-color 0.15s, box-shadow 0.15s;
  }

  input[type="text"]:focus, input[type="number"]:focus, textarea:focus, select:focus {
    outline: none;
    border-color: var(--focus);
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--focus) 30%, transparent);
  }

  input::placeholder, textarea::placeholder { color: var(--text-muted); opacity: 0.8; }

  textarea {
    font-family: var(--font-mono);
    font-size: 0.78rem;
    resize: vertical;
  }

  select { cursor: pointer; }

  .btn-primary {
    background: var(--accent);
    color: var(--on-accent);
    border: 1px solid var(--accent);
    padding: 8px 18px;
    border-radius: var(--radius);
    font: 700 0.85rem var(--font-body);
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s;
    margin-left: auto;
  }

  .btn-primary:hover { background: var(--accent-hover); border-color: var(--accent-hover); }
  .btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }

  .btn-sm {
    background: transparent;
    border: 1px solid var(--border-strong);
    color: var(--text);
    padding: 4px 10px;
    border-radius: var(--radius-sm);
    font: 600 0.75rem var(--font-body);
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s;
  }

  .btn-sm:hover { background: var(--hover); border-color: var(--text); }
  .btn-sm:disabled { opacity: 0.4; cursor: not-allowed; }

  .btn-danger { border-color: var(--danger); color: var(--danger); }
  .btn-danger:hover { background: var(--danger-bg); border-color: var(--danger); }

  .project-config-edit label {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 0.8rem;
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

  .live-title { margin: 12px 0 6px; font-size: 0.85rem; color: var(--text-muted); }
  .live-output { font-size: 0.7rem; white-space: pre; overflow-x: auto; }

  .log-output {
    flex: 1;
    overflow-y: auto;
    background: var(--terminal-bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    padding: 14px;
    font-family: var(--font-mono);
    font-size: 0.78rem;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--terminal-text);
  }

  .empty {
    text-align: center;
    color: var(--text-muted);
    padding: 40px;
    font-size: 0.9rem;
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
    font-size: 0.8rem;
  }
  .version {
    font-family: var(--font-mono);
    font-size: 0.75rem;
    color: var(--text-muted);
  }

  .hint {
    font-size: 0.78rem;
    color: var(--text-muted);
    line-height: 1.45;
  }

  .actions { margin-left: auto; }

  .search-box {
    flex: 1;
    max-width: 250px;
    margin: 0 12px;
  }

  .search-input {
    width: 100%;
    padding: 6px 10px;
    font-size: 0.8rem;
  }

  .confirm-item {
    padding: 10px 14px;
    gap: 10px;
  }

  .confirm-item .name { font-weight: 600; }

  .branch-name-input {
    flex: 1;
    max-width: 300px;
    font-family: var(--font-mono);
    font-size: 0.78rem;
  }

  .cleanup-note {
    font-size: 0.8rem;
    color: var(--text-muted);
    font-style: italic;
  }

  .confirm-result {
    font-family: var(--font-mono);
    font-size: 0.78rem;
    color: var(--text-muted);
  }

  .merge-msg {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 0.78rem;
    color: var(--text-muted);
  }

  .merge-failed { color: var(--danger); }
</style>
