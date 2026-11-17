<script>
  import { tick } from 'svelte';
  import { STEPS } from './tour-steps.js';

  // go(view, tab?) switches the app to the screen a step points at. active is true while the tour is open.
  let { go, active = $bindable(false), onend } = $props();

  let step = $state(-1);
  let rect = $state(null);
  let opener = null;

  $effect(() => { active = step >= 0; });

  export async function start(at = 0) {
    await show(at);
  }

  async function show(i) {
    if (i >= STEPS.length) return end();
    if (step < 0) opener = document.activeElement; // focus goes back here when the tour ends
    step = i;
    go(STEPS[i].view, STEPS[i].tab);
    await tick();
    const adv = document.querySelector('details.advanced');
    if (adv) adv.open = !!STEPS[i].openAdvanced;
    await tick();
    const el = document.querySelector(STEPS[i].sel);
    // a target taller than most of the window is aligned to the top so its start is visible
    el?.scrollIntoView({ block: el.offsetHeight > innerHeight * 0.6 ? 'start' : 'nearest' });
    const r = el?.getBoundingClientRect();
    rect = r ? { top: r.top, left: r.left, width: r.width, height: r.height } : null;
    document.querySelector('.tour-card .btn-primary')?.focus({ preventScroll: true }); // Enter then means Next, once
  }

  function end() {
    step = -1; rect = null;
    go('projects');
    opener?.focus?.(); opener = null;
    try { localStorage.setItem('tourDone', '1'); } catch {}
    onend?.();
  }

  // Whole-pixel positions only: translate(-50%) lands on half pixels and blurs the text.
  function place(r) {
    const w = 320, h = 200, mid = Math.round((innerWidth - w) / 2);
    if (!r) return `left:${mid}px;bottom:24px`;
    const left = Math.round(Math.max(16, Math.min(r.left, innerWidth - w - 16)));
    if (innerHeight - (r.top + r.height) >= h) return `left:${left}px;top:${Math.round(r.top + r.height + 12)}px`;
    if (r.top >= h) return `left:${left}px;bottom:${Math.round(innerHeight - r.top + 12)}px`;
    return `left:${mid}px;bottom:24px`;
  }

  function onkeydown(e) {
    if (step < 0) return;
    if (e.key === 'Escape') end();
    else if (e.key === 'Enter' && e.target instanceof HTMLButtonElement) return; // the focused button handles it
    else if (e.key === 'ArrowRight' || e.key === 'Enter') show(step + 1);
    else if (e.key === 'ArrowLeft' && step > 0) show(step - 1);
  }
</script>

<svelte:window onresize={() => step >= 0 && show(step)} {onkeydown} />

{#if step >= 0}
  <div class="tour-block"></div>
  {#if rect}
    <div class="tour-spot" style="top:{rect.top - 6}px;left:{rect.left - 6}px;width:{rect.width + 12}px;height:{rect.height + 12}px"></div>
  {:else}
    <div class="tour-dim"></div>
  {/if}
  <div class="tour-card" role="dialog" aria-modal="true" aria-label="Tutorial" style={place(rect)}>
    <div class="tour-count">{step + 1} / {STEPS.length}</div>
    <h3>{STEPS[step].title}</h3>
    <p>{STEPS[step].text}</p>
    <div class="tour-actions">
      <button class="btn-sm" onclick={end}>Skip</button>
      <span class="spacer"></span>
      {#if step > 0}<button class="btn-sm" onclick={() => show(step - 1)}>Back</button>{/if}
      <button class="btn-primary" onclick={() => show(step + 1)}>{step === STEPS.length - 1 ? 'Done' : 'Next'}</button>
    </div>
  </div>
{/if}
