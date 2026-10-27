<script>
  import { onMount } from 'svelte';
  import { DiscoverProjects, GetConfig, SaveConfig, StartUpdate, CancelProject, CancelAll, RenameBranch, CleanupBranch, RetryProjects, GetClaudeDefaults } from '../wailsjs/go/main/App';
  import { EventsOn } from '../wailsjs/runtime/runtime';

  let view = $state('projects');
  let projects = $state([]);
  let selected = $state({});
  let claudeDefaults = $state({ prompt: '', allowedTools: [] });
  let config = $state({ BaseFolder: '', MaxConcurrent: 3, ExcludedProjects: [], Projects: {} });

  let statuses = $state({});
  let logs = $state({});
  let allDone = $state(false);
  let detailProject = $state('');

  let editingProject = $state(null);
  let newProjectName = $state('');

  let isRunning = $state(false);

  let searchFilter = $state('');

  let branchNames = $state({});
  let confirmResults = $state({});
  let confirmDone = $state(false);
  let prevView = $state('');

  onMount(async () => {
    // Register event listeners once
    EventsOn('status-update', (update) => {
      const name = update.project || update.Project || update.ProjectName;
      if (!name) return;
      statuses[name] = update.status || update.Status;
      const cmd = update.command || update.Command;
      const out = update.output || update.Output;
      if (out) {
        logs[name] += cmd + '\n' + out + '\n';
      } else if (cmd) {
        logs[name] += '$ ' + cmd + '\n';
      }
    });

    EventsOn('all-done', () => {
      allDone = true;
      // Pre-fill branch names for successful projects
      Object.keys(statuses).forEach(name => {
        const pc = config.Projects?.[name] || {};
        const pattern = pc.GitNewBranchPattern || 'hotfix/*';
        branchNames[name] = pattern.replace('*', sanitize(name));
      });
      view = 'confirm';
    });

    const loaded = await GetConfig();
    claudeDefaults = await GetClaudeDefaults();
    config = {
      ...loaded, // keep keys this screen doesn't edit (max_attempts etc.)
      Mode: loaded.Mode || 'claude',
      ClaudeModel: loaded.ClaudeModel || 'sonnet',
      ClaudePermissionMode: loaded.ClaudePermissionMode || 'dontAsk',
      ClaudeTimeoutMinutes: loaded.ClaudeTimeoutMinutes || 60,
      ClaudeAllowedTools: loaded.ClaudeAllowedTools?.length ? loaded.ClaudeAllowedTools : [...claudeDefaults.allowedTools],
      ClaudePrompt: loaded.ClaudePrompt || claudeDefaults.prompt,
      BaseFolder: loaded.BaseFolder || '',
      MaxConcurrent: loaded.MaxConcurrent || 3,
      DefaultCommands: loaded.DefaultCommands || [],
      ExcludedProjects: loaded.ExcludedProjects || [],
      Projects: loaded.Projects || {},
    };
    await loadProjects();
  });

  async function loadProjects() {
    try {
      projects = await DiscoverProjects();
      projects.forEach(p => {
        if (!(p.name in selected)) selected[p.name] = false;
      });
    } catch (e) {
      projects = [];
    }
  }

  function toggleAll() {
    const allSelected = projects.every(p => selected[p.name]);
    projects.forEach(p => selected[p.name] = !allSelected);
  }

  function sanitize(name) {
    return name.toLowerCase().replace(/[^a-z0-9-]/g, '-').replace(/^-+|-+$/g, '');
  }

  function startUpdate() {
    doStart();
  }

  function doStart() {
    const active = n => statuses[n] === 'running' || statuses[n] === 'idle';
    // projects still running keep their state and logs; only new picks are (re)started
    const selectedNames = projects.filter(p => selected[p.name] && !active(p.name)).map(p => p.name);
    if (selectedNames.length === 0) return;
    const keep = Object.keys(statuses).filter(active);
    const kept = Object.fromEntries(keep.map(n => [n, statuses[n]]));
    const keptLogs = Object.fromEntries(keep.map(n => [n, logs[n]]));
    statuses = kept;
    logs = keptLogs;
    allDone = false;
    confirmDone = false;
    confirmResults = {};
    branchNames = {};
    selectedNames.forEach(name => {
      statuses[name] = 'idle';
      logs[name] = '';
    });

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
        GitStartBranch: 'master',
        GitNewBranchPattern: 'hotfix/*',
        IgnoredPackages: [],
        Commands: config.DefaultCommands && config.DefaultCommands.length > 0
          ? [...config.DefaultCommands]
          : [
            'git pull --rebase --autostash',
            'ddev describe >/dev/null 2>&1 || ddev start',
            'ddev composer install',
            'ddev drush deploy -y',
            "ddev composer update 'drupal/core-*' 'symfony/*' 'twig/*' --with-all-dependencies",
            'ddev drush updb -y',
            'ddev drush cex -y',
            'ddev stop',
          ],
      };
      newProjectName = '';
    }
  }

  function deleteProject(name) {
    delete config.Projects[name];
    config.Projects = config.Projects;
    if (editingProject === name) editingProject = null;
  }

  async function runConfirmation() {
    confirmResults = {};
    for (const name of Object.keys(statuses)) {
      try {
        if (statuses[name] === 'success') {
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
    confirmDone = true;
    isRunning = false;
  }

  function retryFailed() {
    const failed = Object.keys(statuses).filter(name =>
      statuses[name] === 'failed' || statuses[name] === 'cancelled'
    );
    if (failed.length === 0) return;

    failed.forEach(name => {
      statuses[name] = 'idle';
      logs[name] = '';
    });

    allDone = false;
    confirmDone = false;
    view = 'running';

    RetryProjects(failed);
  }
</script>

<main>
  <header>
    <h1>◆ Drupal Updater</h1>
    <nav>
      <button class:active={view === 'projects'} onclick={() => view = 'projects'}>Projects</button>
      <button class:active={view === 'config'} onclick={() => view = 'config'}>Settings</button>
      {#if isRunning}
        <button class:active={view === 'running' || view === 'confirm'} onclick={() => view = allDone ? 'confirm' : 'running'}>
          {allDone ? '⚑ Confirm' : '● Running'}
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
        <div class="actions">
          <button class="btn-sm" onclick={toggleAll}>Toggle All</button>
        </div>
      </div>

      <ul class="project-list">
        {#each projects.filter(p => !searchFilter || p.name.toLowerCase().includes(searchFilter.toLowerCase())) as project}
          <li class:selected={selected[project.name]}>
            <label>
              <input type="checkbox" bind:checked={selected[project.name]} />
              <span class="name">{project.name}</span>
              <span class="path">{project.path}</span>
            </label>
          </li>
        {/each}
      </ul>

      {#if projects.length === 0}
        <p class="empty">No projects found. Check your base folder in Settings.</p>
      {/if}

      <div class="footer-actions">
        <button class="btn-primary" onclick={startUpdate} disabled={!projects.some(p => selected[p.name])}>
          Update Selected
        </button>
      </div>
    </section>

  {:else if view === 'config'}
    <section class="panel config-panel">
      <h2>Configuration</h2>

      <div class="config-group">
        <label>
          Base folder
          <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" bind:value={config.BaseFolder} />
        </label>
        <label>
          Max concurrent
          <input type="number" bind:value={config.MaxConcurrent} min="1" max="20" />
        </label>
      </div>

      <div class="config-group" style="margin-top: 12px;">
        <label style="flex: 1;">
          Excluded projects (comma-separated)
          <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" value={config.ExcludedProjects?.join(', ') || ''}
            onchange={(e) => config.ExcludedProjects = e.target.value.split(',').map(s => s.trim()).filter(Boolean)} />
        </label>
      </div>

      {#if config.Mode === 'classic'}
      <div class="config-group" style="margin-top: 12px;">
        <label style="flex: 1;">
          Default commands (one per line)
          <textarea autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" rows="8" value={config.DefaultCommands?.join('\n') || ''}
            onchange={(e) => config.DefaultCommands = e.target.value.split('\n').filter(Boolean)}></textarea>
        </label>
      </div>
      {/if}

      <h3>Claude</h3>
      <div class="config-group">
        <label>
          Mode
          <select bind:value={config.Mode}>
            <option value="claude">claude (tmux session per project)</option>
            <option value="classic">classic (shell commands + auto-heal)</option>
          </select>
        </label>
        <label>
          Model
          <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" bind:value={config.ClaudeModel} />
        </label>
        <label>
          Permission mode
          <select bind:value={config.ClaudePermissionMode}>
            <option value="dontAsk">dontAsk (deny unlisted tools)</option>
            <option value="acceptEdits">acceptEdits</option>
            <option value="plan">plan</option>
            <option value="manual">manual</option>
          </select>
        </label>
        <label>
          Timeout (min)
          <input type="number" bind:value={config.ClaudeTimeoutMinutes} min="1" />
        </label>
      </div>
      <div class="config-group" style="margin-top: 12px;">
        <label style="flex: 1;">
          Allowed tools (one per line)
          <textarea autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" rows="5" value={config.ClaudeAllowedTools?.join('\n') || ''}
            onchange={(e) => config.ClaudeAllowedTools = e.target.value.split('\n').map(s => s.trim()).filter(Boolean)}></textarea>
        </label>
      </div>
      <div class="config-group" style="margin-top: 12px;">
        <label style="flex: 1;">
          Prompt — placeholders: {'{{project}} {{path}} {{start_branch}} {{temp_branch}} {{ignored_packages}} {{project_instructions}} {{result_file}}'}
          <textarea autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" rows="16" bind:value={config.ClaudePrompt}></textarea>
        </label>
        <button class="btn-sm" onclick={() => config.ClaudePrompt = claudeDefaults.prompt}>Reset prompt to default</button>
      </div>

      <h3>Project Overrides</h3>
      <div class="project-configs">
        {#each Object.keys(config.Projects || {}) as name}
          <div class="project-config-item">
            <div class="project-config-header">
              <strong>{name}</strong>
              <div>
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
                  <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" bind:value={config.Projects[name].GitStartBranch} />
                </label>
                <label>
                  Branch pattern
                  <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" bind:value={config.Projects[name].GitNewBranchPattern} />
                </label>
                <label>
                  Ignored packages (comma-separated)
                  <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" value={config.Projects[name].IgnoredPackages?.join(', ') || ''}
                    onchange={(e) => config.Projects[name].IgnoredPackages = e.target.value.split(',').map(s => s.trim()).filter(Boolean)} />
                </label>
                {#if config.Mode === 'classic'}
                <label>
                  Commands (one per line)
                  <textarea autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" rows="6" value={config.Projects[name].Commands?.join('\n') || ''}
                    onchange={(e) => config.Projects[name].Commands = e.target.value.split('\n').filter(Boolean)}></textarea>
                </label>
                {:else}
                <label>
                  Extra instructions for Claude (added to the prompt)
                  <textarea autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" rows="5" bind:value={config.Projects[name].ClaudeInstructions}></textarea>
                </label>
                {/if}
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

      <div class="footer-actions">
        <button class="btn-primary" onclick={saveConfig}>Save Configuration</button>
      </div>
    </section>

  {:else if view === 'running'}
    <section class="panel">
      <div class="panel-header">
        <h2>Updating</h2>
        {#if allDone}
          <span class="badge badge-success">All done</span>
        {:else}
          <span class="badge badge-running">In progress...</span>
          <button class="btn-sm btn-danger" onclick={() => CancelAll()}>Cancel All</button>
        {/if}
      </div>

      <ul class="status-list">
        {#each Object.keys(statuses) as name}
          <li class="status-item">
            <button class="status-btn" onclick={() => { prevView = 'running'; detailProject = name; view = 'detail'; }}>
              <span class="status-indicator status-{statuses[name]}"></span>
              <span class="status-label">{statuses[name].toUpperCase()}</span>
              <span class="name">{name}</span>
            </button>
            {#if statuses[name] === 'running'}
              <button class="btn-sm btn-danger cancel-btn" onclick={(e) => { e.stopPropagation(); CancelProject(name); }}>Cancel</button>
            {/if}
          </li>
        {/each}
      </ul>
    </section>

  {:else if view === 'detail'}
    <section class="panel">
      <div class="panel-header">
        <button class="btn-sm" onclick={() => view = prevView || 'running'}>← Back</button>
        <h2>{detailProject}</h2>
        <span class="badge badge-{statuses[detailProject]}">{statuses[detailProject]?.toUpperCase()}</span>
      </div>
      <pre class="log-output">{logs[detailProject] || 'Waiting...'}</pre>
    </section>

  {:else if view === 'confirm'}
    <section class="panel">
      <div class="panel-header">
        <h2>Confirm Branch Names</h2>
        {#if confirmDone}
          <span class="badge badge-success">Complete</span>
        {/if}
      </div>

      <ul class="status-list">
        {#each Object.keys(statuses) as name}
          <li class="status-item confirm-item">
            <span class="status-indicator status-{statuses[name]}"></span>
            <span class="name">{name}</span>
            {#if confirmDone}
              <span class="confirm-result">{confirmResults[name] || ''}</span>
            {:else if statuses[name] === 'success'}
              <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" bind:value={branchNames[name]} class="branch-name-input" />
            {:else}
              <span class="cleanup-note">cleanup will be performed</span>
            {/if}
            <button class="btn-sm" onclick={() => { prevView = 'confirm'; detailProject = name; view = 'detail'; }}>Logs</button>
          </li>
        {/each}
      </ul>

      <div class="footer-actions">
        {#if confirmDone}
          <button class="btn-primary" onclick={() => view = 'projects'}>Done</button>
        {:else}
          {#if Object.values(statuses).some(s => s === 'failed' || s === 'cancelled')}
            <button class="btn-sm btn-danger" onclick={retryFailed}>Retry Failed</button>
          {/if}
          <button class="btn-primary" onclick={runConfirmation}>Confirm</button>
        {/if}
      </div>
    </section>
  {/if}
</main>

<style>
  :global(*) {
    margin: 0;
    padding: 0;
    box-sizing: border-box;
  }

  :global(body) {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
    background: #1a1a2e;
    color: #e0e0e0;
    overflow: hidden;
    height: 100vh;
  }

  main {
    display: flex;
    flex-direction: column;
    height: 100vh;
    padding: 16px;
    gap: 16px;
  }

  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-bottom: 12px;
    border-bottom: 1px solid #2a2a4a;
  }

  h1 {
    font-size: 1.3rem;
    color: #c084fc;
    font-weight: 700;
  }

  h2 {
    font-size: 1rem;
    color: #e0e0e0;
    font-weight: 600;
  }

  h3 {
    font-size: 0.85rem;
    color: #a0a0a0;
    margin-top: 16px;
    margin-bottom: 8px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  nav {
    display: flex;
    gap: 4px;
  }

  nav button {
    background: transparent;
    border: 1px solid #3a3a5a;
    color: #a0a0a0;
    padding: 6px 14px;
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.8rem;
    transition: all 0.15s;
  }

  nav button:hover {
    background: #2a2a4a;
    color: #e0e0e0;
  }

  nav button.active {
    background: #c084fc22;
    border-color: #c084fc;
    color: #c084fc;
  }

  .panel {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 12px;
    overflow: hidden;
  }

  /* Settings is long: scroll the whole panel instead of clipping it */
  .config-panel {
    overflow-y: auto;
    display: block;
  }
  .config-panel > * {
    margin-bottom: 12px;
  }
  .config-panel .project-configs {
    overflow: visible;
  }
  .config-panel .footer-actions {
    position: sticky;
    bottom: 0;
    background: #1a1a2e;
    padding: 8px 0;
  }

  .panel-header {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .project-list, .status-list {
    list-style: none;
    flex: 1;
    overflow-y: auto;
    border: 1px solid #2a2a4a;
    border-radius: 8px;
    background: #16162a;
  }

  .project-list li {
    border-bottom: 1px solid #2a2a4a;
    transition: background 0.1s;
  }

  .project-list li:last-child { border-bottom: none; }

  .project-list li:hover {
    background: #1e1e3a;
  }

  .project-list li.selected {
    background: #c084fc08;
  }

  .project-list label {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 14px;
    cursor: pointer;
  }

  .project-list input[type="checkbox"] {
    accent-color: #c084fc;
    width: 16px;
    height: 16px;
  }

  .project-list .name {
    font-weight: 500;
    color: #e0e0e0;
  }

  .project-list .path {
    font-size: 0.75rem;
    color: #666;
    margin-left: auto;
  }

  .status-list li {
    border-bottom: 1px solid #2a2a4a;
  }

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
    cursor: pointer;
    text-align: left;
    transition: background 0.1s;
  }

  .status-btn:hover { background: #1e1e3a; }

  .status-indicator {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .status-idle { background: #555; }
  .status-running { background: #f59e0b; animation: pulse 1s infinite; }
  .status-success { background: #4ade80; }
  .status-failed { background: #ef4444; }
  .status-cancelled { background: #888; }

  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.4; }
  }

  .status-label {
    font-size: 0.7rem;
    font-weight: 700;
    letter-spacing: 0.05em;
    width: 70px;
    color: #a0a0a0;
  }

  .badge {
    font-size: 0.7rem;
    padding: 3px 8px;
    border-radius: 4px;
    font-weight: 600;
  }

  .badge-success { background: #4ade8022; color: #4ade80; }
  .badge-running { background: #f59e0b22; color: #f59e0b; }
  .badge-failed { background: #ef444422; color: #ef4444; }
  .badge-idle { background: #55555522; color: #888; }

  .footer-actions {
    display: flex;
    align-items: center;
    gap: 12px;
    padding-top: 12px;
    border-top: 1px solid #2a2a4a;
  }

  input[type="text"], input[type="number"], textarea {
    background: #16162a;
    border: 1px solid #3a3a5a;
    color: #e0e0e0;
    padding: 8px 10px;
    border-radius: 6px;
    font-size: 0.85rem;
    outline: none;
    transition: border-color 0.15s;
  }

  input:focus, textarea:focus {
    border-color: #c084fc;
  }

  textarea {
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 0.8rem;
    resize: vertical;
  }

  .btn-primary {
    background: #c084fc;
    color: #1a1a2e;
    border: none;
    padding: 8px 18px;
    border-radius: 6px;
    font-weight: 600;
    font-size: 0.85rem;
    cursor: pointer;
    transition: background 0.15s;
    margin-left: auto;
  }

  .btn-primary:hover { background: #a855f7; }
  .btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }

  .btn-sm {
    background: transparent;
    border: 1px solid #3a3a5a;
    color: #a0a0a0;
    padding: 4px 10px;
    border-radius: 4px;
    font-size: 0.75rem;
    cursor: pointer;
    transition: all 0.15s;
  }

  .btn-sm:hover { background: #2a2a4a; color: #e0e0e0; }

  .btn-danger { border-color: #ef444466; color: #ef4444; }
  .btn-danger:hover { background: #ef444422; }

  .config-group {
    display: flex;
    gap: 16px;
  }

  .config-group label {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 0.8rem;
    color: #a0a0a0;
  }

  .project-configs {
    flex: 1;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .project-config-item {
    border: 1px solid #2a2a4a;
    border-radius: 6px;
    padding: 10px 14px;
    background: #16162a;
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

  .project-config-edit label {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 0.8rem;
    color: #a0a0a0;
  }

  .add-project {
    display: flex;
    gap: 8px;
    align-items: center;
  }

  select {
    background: #16162a;
    border: 1px solid #3a3a5a;
    color: #e0e0e0;
    padding: 8px 10px;
    border-radius: 6px;
    font-size: 0.85rem;
    outline: none;
    transition: border-color 0.15s;
    cursor: pointer;
  }

  select:focus {
    border-color: #c084fc;
  }

  .log-output {
    flex: 1;
    overflow-y: auto;
    background: #0d0d1a;
    border: 1px solid #2a2a4a;
    border-radius: 8px;
    padding: 14px;
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 0.78rem;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-all;
    color: #a0e0a0;
  }

  .empty {
    text-align: center;
    color: #666;
    padding: 40px;
    font-size: 0.9rem;
  }

  .actions {
    margin-left: auto;
  }

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

  .branch-name-input {
    flex: 1;
    max-width: 300px;
  }

  .cleanup-note {
    font-size: 0.8rem;
    color: #888;
    font-style: italic;
  }

  .confirm-result {
    font-size: 0.8rem;
    color: #a0a0a0;
  }
</style>
