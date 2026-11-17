// Appearance choices shared by the welcome card and Settings → Appearance.
export const FONTS = {
  atkinson: { label: 'Atkinson Hyperlegible', scale: 1.125, css: "'Atkinson Hyperlegible Next Variable', 'Atkinson Hyperlegible Next'" },
  jetbrains: { label: 'JetBrains Mono', scale: 1, css: "'JetBrains Mono Variable', 'JetBrains Mono'" },
  spacegrotesk: { label: 'Space Grotesk', scale: 1.1, css: "'Space Grotesk Variable', 'Space Grotesk'" },
};
export const THEMES = [['system', 'System'], ['light', 'Light'], ['dark', 'Dark']];
export const SIZES = [['14', 'Small'], ['16', 'Default'], ['18', 'Large'], ['20', 'Extra large']];
export const FONT_ITEMS = Object.entries(FONTS).map(([k, f]) => [k, f.label, f.css]);

// localStorage can throw or be empty (private window, blocked site data): always fall back to the default
export const load = (key, ok, fallback) => { try { const v = localStorage.getItem(key); return ok(v) ? v : fallback; } catch { return fallback; } };
