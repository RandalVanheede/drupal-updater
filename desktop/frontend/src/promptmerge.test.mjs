// Run: node desktop/frontend/src/promptmerge.test.mjs
import { diffBlocks, mergeText, hasConflicts } from './promptmerge.js';
import assert from 'node:assert';
const mine = 'a\nb custom\nc\nd', theirs = 'a\nb\nc\nnew\nd';
const bl = diffBlocks(mine, theirs);
assert.equal(bl.filter(b => !b.same).length, 2);
assert.equal(mergeText(bl, ['mine', 'theirs']), 'a\nb custom\nc\nnew\nd');
assert.equal(mergeText(bl, ['both', 'mine']), 'a\nb custom\nb\nc\nd');
assert.ok(hasConflicts(mergeText(bl, [])));
assert.equal(mergeText(diffBlocks(theirs, theirs), []), theirs);
assert.equal(mergeText(diffBlocks('', 'x'), ['theirs']), 'x');
console.log('ok');
