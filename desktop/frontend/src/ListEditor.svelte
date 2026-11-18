<script>
  // A list of strings edited one entry at a time: type (or browse) and Add, remove with the x button.
  // ordered = the order matters (first match wins): entries show their rank and can be moved up or down.
  // The parent owns the array: it gets the new one through onchange.
  let { items = [], onchange, placeholder = '', onbrowse = null, ordered = false, emptyText = 'Nothing added yet', label = '' } = $props();
  let draft = $state('');

  const list = $derived(items || []);
  function add(value) {
    const v = (value ?? draft).trim();
    if (!v) return;
    if (!list.includes(v)) onchange([...list, v]);
    draft = '';
  }
  async function browse() {
    const picked = await onbrowse();
    if (picked) add(picked);
  }
  function remove(i) { onchange(list.filter((_, j) => j !== i)); }
  function move(i, by) {
    const next = [...list];
    [next[i], next[i + by]] = [next[i + by], next[i]];
    onchange(next);
  }
  const RANKS = ['1st choice', '2nd choice', '3rd choice'];
</script>

<div class="list-editor">
  <div class="add-row">
    <input type="text" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" aria-label={label || placeholder}
      {placeholder} bind:value={draft} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); add(); } }} />
    {#if onbrowse}<button type="button" class="btn-sm" onclick={browse}>Browse…</button>{/if}
    <button type="button" class="btn-sm" onclick={() => add()} disabled={!draft.trim()}>Add</button>
  </div>

  {#if list.length}
    <ul class="entries" aria-label={label}>
      {#each list as item, i (item)}
        <li>
          {#if ordered}<span class="rank" title={i === 0 ? 'Used when it exists' : 'Used only if the ones above do not exist'}>{RANKS[i] ?? `${i + 1}th choice`}</span>{/if}
          <span class="value" title={item}><bdi>{item}</bdi></span>
          {#if ordered && list.length > 1}
            <button type="button" class="icon" aria-label="Move {item} up" disabled={i === 0} onclick={() => move(i, -1)}>▲</button>
            <button type="button" class="icon" aria-label="Move {item} down" disabled={i === list.length - 1} onclick={() => move(i, 1)}>▼</button>
          {/if}
          <button type="button" class="icon remove" aria-label="Remove {item}" onclick={() => remove(i)}>✕</button>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="empty-note">{emptyText}</p>
  {/if}
</div>

<style>
  .list-editor { display: grid; gap: 8px; min-width: 0; }
  .add-row { display: flex; gap: 8px; }
  .add-row input {
    flex: 1; min-width: 0;
    background: var(--surface); border: 1px solid var(--border-strong); color: var(--text);
    padding: 8px 10px; border-radius: var(--radius); font-family: var(--font-body); font-size: var(--fs0);
  }
  .add-row input:focus { outline: none; border-color: var(--focus); box-shadow: 0 0 0 2px color-mix(in srgb, var(--focus) 30%, transparent); }
  .add-row input::placeholder { color: var(--text-muted); opacity: 0.8; }
  .entries { list-style: none; margin: 0; padding: 0; border: 1px solid var(--border); border-radius: var(--radius); background: var(--bg); overflow: hidden; }
  li { display: flex; align-items: center; gap: 8px; padding: 6px 8px 6px 12px; border-bottom: 1px solid var(--border); }
  li:last-child { border-bottom: none; }
  .value { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font: var(--fs0) var(--font-mono); direction: rtl; text-align: left; }
  .rank { flex: none; font-size: var(--fs-1); font-weight: 700; color: var(--accent); min-width: 5.5rem; }
  .icon { flex: none; width: 1.8rem; height: 1.8rem; display: grid; place-items: center; background: none; border: 0; border-radius: 6px; color: var(--text-muted); font-size: var(--fs-1); cursor: pointer; }
  .icon:hover:not(:disabled) { background: var(--hover); color: var(--text); }
  .icon:disabled { opacity: 0.3; cursor: default; }
  .remove { font-size: var(--fs0); }
  .remove:hover:not(:disabled) { background: var(--danger-bg); color: var(--danger); }
  .empty-note { margin: 0; font-size: var(--fs0); font-weight: 400; color: var(--text-muted); }
</style>
