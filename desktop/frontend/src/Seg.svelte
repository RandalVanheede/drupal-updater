<script>
  import { segKeys } from './keys.js';
  // items: [value, text, fontFamily?, dotClass?]. labelled = wrapped in a .field with its label above.
  let { label, items, value, onpick, small = false, labelled = true } = $props();
</script>

{#snippet control()}
  <div class="seg" class:seg-sm={small} role="radiogroup" aria-label={label} onkeydown={segKeys}>
    {#each items as [v, text, face, dot]}
      <button type="button" role="radio" tabindex={value === v ? 0 : -1} aria-checked={value === v} class:on={value === v} style:font-family={face} onclick={() => onpick(v)}>{#if dot}<i class="dot dot-{dot}"></i>{/if}{text}</button>
    {/each}
  </div>
{/snippet}

{#if labelled}
  <div class="field"><span>{label}</span>{@render control()}</div>
{:else}
  {@render control()}
{/if}
