<script>
  // The final report of a run, as readable text: blank-line separated blocks, an optional "Heading:" line,
  // "- " bullets and the composer.lock table. Everything is rendered as text, never as HTML.
  import { parseResult } from './result.js';
  let { status, text } = $props();
  const TITLE = { failed: 'Why it failed', success: 'What was done', skipped: 'Nothing to update', cancelled: 'Cancelled' };
  const blocks = $derived(parseResult(text));
</script>

<details class="result result-{status}" open>
  <summary><h3>{TITLE[status] || status}</h3></summary>
  {#each blocks as b}
    {#if b.heading}<h4>{b.heading}</h4>{/if}
    {#if b.table}
      <div class="table-wrap">
        <table>
          <thead><tr>{#each b.table[0] as c}<th>{c}</th>{/each}</tr></thead>
          <tbody>{#each b.table.slice(1) as r}<tr>{#each r as c}<td>{c}</td>{/each}</tr>{/each}</tbody>
        </table>
      </div>
    {/if}
    {#each b.items as it}
      {#if it.bullet}<p class="li">{it.text}</p>{:else}<p>{it.text}</p>{/if}
    {/each}
  {/each}
</details>

<style>
  .result {
    border: 1px solid var(--border-strong);
    border-left: 4px solid var(--text-muted);
    border-radius: var(--radius);
    padding: 12px 16px;
    margin-bottom: 12px;
    font-size: var(--fs0);
    line-height: 1.5;
    max-height: 55vh;
    overflow-y: auto;
  }
  .result-failed { border-left-color: var(--danger); }
  .result-success { border-left-color: var(--success); }
  summary { cursor: pointer; }
  h3 { display: inline; font-size: var(--fs1); }
  details[open] > summary { margin-bottom: 6px; }
  h4 { margin: 12px 0 4px; font-size: var(--fs0); color: var(--text-muted); }
  p { margin: 2px 0; overflow-wrap: anywhere; }
  .li { padding-left: 1.1em; text-indent: -0.8em; }
  .li::before { content: '•'; display: inline-block; width: 0.8em; text-indent: 0; }
  .table-wrap { overflow-x: auto; }
  table { border-collapse: collapse; font-family: var(--font-mono); font-size: var(--fs-1); }
  th, td { text-align: left; padding: 2px 12px 2px 0; white-space: nowrap; }
  th { color: var(--text-muted); font-weight: 600; }
</style>
