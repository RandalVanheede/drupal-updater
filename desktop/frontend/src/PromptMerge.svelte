<script>
  import Seg from './Seg.svelte';
  import { diffBlocks, mergeText, hasConflicts } from './promptmerge.js';
  // A custom Claude prompt meets a changed built-in prompt: pick per difference, edit the result, save.
  let { merge, onsave, onlater } = $props();

  const blocks = diffBlocks(merge.mine, merge.theirs);
  const hunks = blocks.filter(b => !b.same);
  let picks = $state([]);
  let result = $state(mergeText(blocks, []));
  let edited = $state(false); // once typed in, picks no longer rewrite the result
  let error = $state('');
  let saving = $state(false);
  const conflicts = $derived(hasConflicts(result));

  function pick(i, v) {
    picks[i] = v;
    if (!edited) result = mergeText(blocks, picks);
  }
  function pickAll(v) {
    picks = hunks.map(() => v);
    edited = false;
    result = mergeText(blocks, picks);
  }
  async function save() {
    error = '';
    saving = true;
    try { await onsave(result); } catch (e) { error = String(e?.message || e); }
    saving = false;
  }
  const PICKS = [['mine', 'Yours'], ['theirs', 'New'], ['both', 'Both']];
</script>

<div class="tour-dim"></div>
<div class="welcome-wrap">
  <div class="tour-card merge" role="dialog" aria-modal="true" aria-labelledby="merge-title">
    <h3 id="merge-title">The built-in Claude prompt changed</h3>
    {#if merge.source === 'backup'}
      <p>Your custom prompt from before 1.4 was set aside when the app took over the fixed update steps. Take what you still need from it into the new prompt. Steps the app now runs itself (checkout, ddev start, composer install, drush deploy, updb, cex) are better left out.</p>
    {:else}
      <p>Your custom prompt was written for an older built-in prompt; it is still used until you decide. For each difference, keep yours, take the new text, or both. Then check the final version below and save.</p>
    {/if}

    <div class="merge-bar">
      <span>{hunks.length} difference{hunks.length === 1 ? '' : 's'}</span>
      <span class="spacer"></span>
      <button class="btn-sm" onclick={() => pickAll('mine')}>All yours</button>
      <button class="btn-sm" onclick={() => pickAll('theirs')}>All new</button>
    </div>

    <div class="merge-hunks">
      {#each hunks as h, i}
        <div class="merge-hunk" class:decided={picks[i]}>
          <div class="merge-sides">
            <div class:chosen={picks[i] === 'mine' || picks[i] === 'both'}>
              <span class="hint">Yours</span>
              <pre>{h.mine.join('\n') || '(nothing)'}</pre>
            </div>
            <div class:chosen={picks[i] === 'theirs' || picks[i] === 'both'}>
              <span class="hint">New default</span>
              <pre>{h.theirs.join('\n') || '(nothing)'}</pre>
            </div>
          </div>
          <Seg label="Difference {i + 1}" labelled={false} small items={PICKS} value={picks[i]} onpick={v => pick(i, v)} />
        </div>
      {/each}
    </div>

    <label class="merge-result">
      <span>Final version {#if edited}<span class="hint">edited by hand: picks no longer change it · <button class="link" onclick={() => { edited = false; result = mergeText(blocks, picks); }}>rebuild from picks</button></span>{/if}</span>
      <textarea rows="10" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" bind:value={result} oninput={() => edited = true}></textarea>
    </label>
    {#if conflicts}<p class="hint">Undecided differences are marked with &lt;&lt;&lt;&lt;&lt;&lt;&lt; and &gt;&gt;&gt;&gt;&gt;&gt;&gt;. Pick one above or edit them out.</p>{/if}
    {#if error}<p class="error">{error}</p>{/if}

    <div class="tour-actions">
      <button class="btn-sm" onclick={onlater}>Decide later</button>
      <span class="spacer"></span>
      <button class="btn-primary" onclick={save} disabled={conflicts || saving}>Save final version</button>
    </div>
  </div>
</div>

<style>
  .merge { position: static; width: min(980px, calc(100vw - 32px)); max-height: calc(100vh - 48px); overflow: auto; pointer-events: auto; }
  .merge-bar { display: flex; gap: 8px; align-items: center; margin: 10px 0; font-size: var(--fs-1); color: var(--text-muted); }
  .spacer { flex: 1; }
  .merge-hunks { display: grid; gap: 10px; }
  .merge-hunk { border: 1px solid var(--border-strong); border-radius: var(--radius-sm); padding: 8px; }
  .merge-hunk.decided { border-color: var(--border); opacity: 0.85; }
  .merge-sides { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; margin-bottom: 6px; }
  .merge-sides > div { border-radius: var(--radius-sm); padding: 4px 6px; background: var(--hover); }
  .merge-sides > div.chosen { outline: 2px solid var(--accent); }
  pre { margin: 2px 0 0; white-space: pre-wrap; word-break: break-word; font-family: var(--font-mono); font-size: var(--fs-1); max-height: 220px; overflow: auto; }
  .merge-result { display: grid; gap: 4px; margin-top: 12px; }
  .merge-result textarea { font-family: var(--font-mono); font-size: var(--fs-1); }
  .error { color: var(--danger); }
  .link { background: none; border: 0; padding: 0; color: var(--accent); text-decoration: underline; cursor: pointer; font: inherit; }
  @media (max-width: 700px) { .merge-sides { grid-template-columns: 1fr; } }
</style>
