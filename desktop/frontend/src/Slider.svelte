<script>
  // A range input with a live value label. tone(value) -> 'ok' | 'mid' | 'high' | 'max' tints the thumb and the
  // description; gradient = a coloured track (low to high) for settings where more costs more.
  let { label, value, onchange, min, max, step = 1, format = (v) => String(v), describe = null, tone = null, gradient = false } = $props();
  // an existing value outside the slider's range is shown clamped but only changed when the user moves the slider
  const shown = $derived(Math.min(Math.max(Number(value) || min, min), max));
  const t = $derived(tone ? tone(shown) : '');
  const pct = $derived(((shown - min) / (max - min)) * 100);
</script>

<div class="field slider">
  <span class="head"><span>{label}</span><output class="val {t}">{format(shown)}</output></span>
  <input type="range" {min} {max} {step} value={shown} class:gradient style="--pct:{pct}%" aria-label={label} aria-valuetext={format(shown)}
    oninput={(e) => onchange(Number(e.currentTarget.value))} />
  {#if describe}<span class="desc {t}">{describe(shown)}</span>{/if}
</div>

<style>
  .slider { --tone: var(--accent); gap: 6px; }
  .head { display: flex; justify-content: space-between; align-items: baseline; gap: 8px; }
  .val { font: 700 var(--fs0) var(--font-mono); color: var(--text); }
  .desc { font-size: var(--fs-1); color: var(--text-muted); font-weight: 400; }
  .ok { --tone: var(--success); } .mid { --tone: var(--warn); } .high { --tone: var(--orange); } .max { --tone: var(--danger); }
  .desc.ok, .desc.mid, .desc.high, .desc.max { color: var(--tone); font-weight: 600; }

  input[type='range'] { -webkit-appearance: none; appearance: none; width: 100%; height: 22px; background: transparent; padding: 0; border: 0; cursor: pointer; }
  input[type='range']::-webkit-slider-runnable-track { height: 8px; border-radius: 4px; background: linear-gradient(to right, var(--accent) var(--pct), var(--border-strong) var(--pct)); }
  input[type='range'].gradient::-webkit-slider-runnable-track { background: linear-gradient(to right, var(--success), var(--warn) 55%, var(--orange) 75%, var(--danger)); opacity: 0.85; }
  input[type='range']::-webkit-slider-thumb { -webkit-appearance: none; width: 20px; height: 20px; margin-top: -6px; border-radius: 50%; background: var(--surface); border: 3px solid var(--tone); box-shadow: 0 1px 3px rgba(0, 0, 0, 0.35); }
  input[type='range']:focus-visible { outline: none; }
  input[type='range']:focus-visible::-webkit-slider-thumb { outline: 2px solid var(--focus); outline-offset: 2px; }
  input[type='range']::-moz-range-track { height: 8px; border-radius: 4px; background: var(--border-strong); }
  input[type='range']::-moz-range-progress { height: 8px; border-radius: 4px; background: var(--accent); }
  input[type='range'].gradient::-moz-range-track { background: linear-gradient(to right, var(--success), var(--warn) 55%, var(--orange) 75%, var(--danger)); }
  input[type='range']::-moz-range-thumb { width: 14px; height: 14px; border-radius: 50%; background: var(--surface); border: 3px solid var(--tone); }
</style>
