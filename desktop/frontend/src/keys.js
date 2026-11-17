// Arrow keys move through a segmented control or tab list (roving tabindex, one tab stop).
export function segKeys(e) {
  const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
  if (!step) return;
  const items = [...e.currentTarget.querySelectorAll('button')];
  const next = items[(items.indexOf(document.activeElement) + step + items.length) % items.length];
  next.click(); next.focus();
  e.preventDefault();
}
