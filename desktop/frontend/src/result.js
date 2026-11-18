// parseResult splits a run's final report into blocks: { heading, items: [{ bullet, text }], table }.
export function parseResult(text) {
  return (text || '').trim().split(/\n\s*\n/).map(block => {
    const lines = block.split('\n').map(l => l.trimEnd()).filter(l => l.trim());
    const b = { heading: '', items: [], table: null };
    if (lines.length && /:$/.test(lines[0]) && !/^\s*[-*] /.test(lines[0])) b.heading = lines.shift().slice(0, -1);
    const rows = lines.filter(l => l.trim().startsWith('|'));
    if (rows.length) {
      b.table = rows.filter(r => !/^\|[\s|-]+\|$/.test(r.trim()))
        .map(r => r.trim().slice(1, -1).split('|').map(c => c.trim()));
    }
    for (const l of lines) {
      if (l.trim().startsWith('|')) continue;
      const m = l.match(/^\s*[-*] (.*)$/);
      b.items.push(m ? { bullet: true, text: m[1] } : { bullet: false, text: l.trim() });
    }
    return b;
  }).filter(b => b.heading || b.items.length || b.table);
}
