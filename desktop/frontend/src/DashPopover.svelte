<script>
  import { BrowserOpenURL } from '../wailsjs/runtime/runtime';
  // Detail card for one project, anchored to the element at rect: what the repository says (folder, core version,
  // the version it moves to) and, when the project is matched to the applications dashboard, what that says. It
  // stays open while the pointer is over it (so its list can be scrolled and its link clicked).
  let { project, status = null, info = null, rect, pinned = false, onenter, onleave, onclose, onsave } = $props();

  const W = 440;
  let card = $state();
  let height = $state(0);
  // below the anchor when it fits, else above, else pinned to the bottom edge; always inside the window
  const pos = $derived.by(() => {
    const h = height || 300;
    const left = Math.round(Math.max(12, Math.min(rect.left - W + rect.width, innerWidth - W - 12)));
    let top;
    if (innerHeight - rect.bottom - 12 >= h) top = rect.bottom + 6;
    else if (rect.top - 12 >= h) top = rect.top - h - 6;
    else top = innerHeight - h - 12;
    return `left:${left}px;top:${Math.round(Math.max(8, top))}px;max-height:${Math.round(innerHeight - 16)}px`;
  });
  $effect(() => { if (card) height = card.offsetHeight; });

  const core = (m) => (m === 'drupal' ? 'Drupal core' : m);
  const sec = $derived(info?.securityUpdates || []);
  const ours = $derived(project?.core || '');
  const STATE = { security: 'security update available', behind: 'update available', current: 'up to date' };
</script>

<svelte:window onkeydown={(e) => { if (e.key === 'Escape') onclose(); }} />

<div bind:this={card} class="pop" class:pinned role="dialog" aria-label="Details for {project?.name}" style={pos} onmouseenter={onenter} onmouseleave={onleave}>
  <div class="head">
    <strong>{info?.name || project?.name}</strong>
    {#if info?.url}<button type="button" class="btn-sm" onclick={() => BrowserOpenURL(info.url)}>Open in dashboard ↗</button>{/if}
  </div>
  <dl>
    <dt>Folder</dt><dd class="mono">{project?.path || '–'}</dd>
    <dt>Drupal core</dt>
    <dd>
      {#if ours && status && status.status !== 'current'}<span class="mono">{ours} → {status.latest}</span> <span class="muted">{STATE[status.status]}</span>
      {:else if ours}<span class="mono">{ours}</span>{status ? ' ' : ''}{#if status}<span class="muted">{STATE[status.status]}</span>{/if}
      {:else}<span class="muted">no composer.lock found</span>{/if}
      {#if ours && info?.coreVersion && ours !== info.coreVersion}<br /><span class="muted">the dashboard last saw {info.coreVersion}</span>{/if}
    </dd>
    {#if info}
      <dt>Customer</dt><dd>{info.customer || '–'}</dd>
      <dt>SLA</dt><dd>{info.sla || 'none'}</dd>
      <dt>Account manager</dt><dd>{info.accountManager || '–'}</dd>
      <dt>PHP</dt><dd>{info.php || '–'}</dd>
      <dt>Last scan</dt><dd>{info.lastScan || '–'}</dd>
    {/if}
  </dl>

  {#if info}
    {#if info.matchReason}
      <p class="muted why">Matched because {info.matchReason}{info.autoMatched ? ' (detected automatically)' : ''}.
        {#if info.autoMatched && onsave}<button type="button" class="btn-sm" onclick={onsave}>Save this match</button>{/if}</p>
    {/if}
    {#if info.stale}<p class="warn">The site has not reported to the dashboard for days, so the module data below can be out of date.</p>{/if}
    {#if info.eolRuntimes?.length}<p class="warn">End of life: {info.eolRuntimes.join(', ')}</p>{/if}

    {#if !info.securityKnown}
      <p class="muted">Pending security updates could not be loaded.</p>
    {:else if sec.length}
      <h4>Pending security updates ({sec.length})</h4>
      <ul>{#each sec as m (m)}<li class:core={m === 'drupal'}>{core(m)}</li>{/each}</ul>
    {:else}
      <p class="ok">No pending security updates.</p>
    {/if}
  {:else}
    <p class="muted">Not linked to a project in the applications dashboard.</p>
  {/if}
  {#if pinned}<p class="muted foot">Pinned: Esc or a click elsewhere closes it.</p>{/if}
</div>

<style>
  .pop {
    position: fixed; z-index: 90; width: 440px; max-width: calc(100vw - 24px); overflow-y: auto; padding: 14px 16px;
    background: var(--surface); color: var(--text); border: 1px solid var(--border-strong); border-radius: 10px;
    box-shadow: 0 12px 32px rgba(0, 0, 0, 0.35); font-size: var(--fs0); line-height: 1.4;
  }
  .pop.pinned { border-color: var(--accent); }
  .head { display: flex; justify-content: space-between; align-items: center; gap: 10px; margin-bottom: 8px; }
  dl { display: grid; grid-template-columns: max-content 1fr; gap: 3px 14px; margin: 0 0 8px; }
  dt { color: var(--text-muted); }
  dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
  .mono { font-family: var(--font-mono); font-size: var(--fs-1); }
  .muted { color: var(--text-muted); font-size: var(--fs-1); }
  h4 { margin: 10px 0 4px; font-size: var(--fs0); color: var(--danger); }
  ul { margin: 0; padding: 0; list-style: none; display: grid; grid-template-columns: repeat(auto-fit, minmax(13rem, 1fr)); gap: 2px 12px; font: var(--fs-1) var(--font-mono); }
  li { overflow-wrap: anywhere; }
  li.core { grid-column: 1 / -1; font-weight: 700; color: var(--danger); }
  .warn { margin: 6px 0; color: var(--warn); }
  .ok { margin: 6px 0 0; color: var(--success); }
  .foot { margin: 8px 0 0; }
  .why { margin: 4px 0 8px; }
</style>
