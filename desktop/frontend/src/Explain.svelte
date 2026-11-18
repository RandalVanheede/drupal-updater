<script>
  // A link-style button that opens an explanation in a modal dialog (Esc, the close button or a click outside closes it).
  let { label, title, children } = $props();
  let dlg;
</script>

<button type="button" class="explain-btn" onclick={() => dlg.showModal()}>{label}</button>
<dialog bind:this={dlg} aria-label={title} onclick={(e) => { if (e.target === dlg) dlg.close(); }}>
  <div class="body">
    <div class="head">
      <h3>{title}</h3>
      <button type="button" class="btn-sm" aria-label="Close" onclick={() => dlg.close()}>✕</button>
    </div>
    {@render children()}
  </div>
</dialog>

<style>
  .explain-btn {
    background: none; border: none; padding: 0; cursor: pointer;
    color: var(--accent); font: inherit; font-size: var(--fs-1); text-decoration: underline;
  }
  dialog {
    margin: auto; max-width: min(640px, calc(100vw - 32px)); max-height: 80vh; padding: 0;
    background: var(--surface); color: var(--text);
    border: 1px solid var(--border-strong); border-radius: var(--radius);
    font-size: var(--fs0); line-height: 1.5;
  }
  dialog::backdrop { background: rgb(0 0 0 / 0.45); }
  .body { padding: 16px 20px; }
  .head { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-bottom: 8px; }
  h3 { margin: 0; font-size: var(--fs1); }
  .body :global(dt) { font-family: var(--font-mono); font-size: var(--fs-1); margin-top: 10px; }
  .body :global(dd) { margin: 2px 0 0; color: var(--text-muted); }
  .body :global(p), .body :global(ul) { margin: 8px 0; }
  .body :global(ul) { padding-left: 1.3em; }
  .body :global(code) { font-family: var(--font-mono); font-size: var(--fs-1); }
</style>
