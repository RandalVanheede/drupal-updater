<script>
  import { onMount, tick } from 'svelte';
  import Seg from './Seg.svelte';
  import { THEMES, SIZES, FONT_ITEMS } from './appearance.js';
  // First run: the appearance picker. Changes apply live behind the dialog (the parent reacts to the bound values).
  let { theme = $bindable(), font = $bindable(), fontSize = $bindable(), ondone } = $props();
  onMount(() => { tick().then(() => document.querySelector('.welcome .btn-primary')?.focus()); });
</script>

<div class="tour-dim"></div>
<div class="welcome-wrap">
  <div class="tour-card welcome" role="dialog" aria-modal="true" aria-label="Welcome">
    <h3>Welcome to Drupal Updater</h3>
    <p>First, make it comfortable. Changes apply right away and you can change them later in Settings.</p>
    <div class="welcome-fields">
      <Seg label="Theme" items={THEMES} value={theme} onpick={v => theme = v} />
      <Seg label="Font" items={FONT_ITEMS} value={font} onpick={v => font = v} />
      <Seg label="Font size" items={SIZES} value={fontSize} onpick={v => fontSize = v} />
    </div>
    <div class="tour-actions">
      <span class="spacer"></span>
      <button class="btn-primary" onclick={ondone}>Continue</button>
    </div>
  </div>
</div>
