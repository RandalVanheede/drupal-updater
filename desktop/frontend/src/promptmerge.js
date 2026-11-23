// Line diff of two prompts into blocks: { same: [lines] } or { mine: [lines], theirs: [lines] } (a hunk to decide).
export function diffBlocks(mine, theirs) {
  const a = mine.split('\n'), b = theirs.split('\n');
  // LCS table from the end, so the walk below goes front to back
  const l = Array.from({ length: a.length + 1 }, () => new Int32Array(b.length + 1));
  for (let i = a.length - 1; i >= 0; i--)
    for (let j = b.length - 1; j >= 0; j--)
      l[i][j] = a[i] === b[j] ? l[i + 1][j + 1] + 1 : Math.max(l[i + 1][j], l[i][j + 1]);
  const blocks = [];
  let hunk = null;
  const flush = () => { if (hunk) blocks.push(hunk); hunk = null; };
  let i = 0, j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) {
      flush();
      const last = blocks[blocks.length - 1];
      if (last?.same) last.same.push(a[i]); else blocks.push({ same: [a[i]] });
      i++; j++;
    } else {
      hunk ??= { mine: [], theirs: [] };
      if (j < b.length && (i === a.length || l[i][j + 1] >= l[i + 1][j])) hunk.theirs.push(b[j++]);
      else hunk.mine.push(a[i++]);
    }
  }
  flush();
  return blocks;
}

// The final text for the picks per hunk ('mine' | 'theirs' | 'both' | undefined); an undecided hunk becomes
// git-style conflict markers, so it shows in the result and blocks saving.
export function mergeText(blocks, picks) {
  const out = [];
  let h = 0;
  for (const b of blocks) {
    if (b.same) { out.push(...b.same); continue; }
    const p = picks[h++];
    if (p === 'mine') out.push(...b.mine);
    else if (p === 'theirs') out.push(...b.theirs);
    else if (p === 'both') out.push(...b.mine, ...b.theirs);
    else out.push('<<<<<<< yours', ...b.mine, '=======', ...b.theirs, '>>>>>>> new default');
  }
  return out.join('\n');
}

export const hasConflicts = (text) => /^(<<<<<<<|>>>>>>>) /m.test(text);
