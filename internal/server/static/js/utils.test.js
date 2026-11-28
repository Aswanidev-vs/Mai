// Viseme timing must follow measured prosody, not just punctuation.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('./utils.js', import.meta.url), 'utf8');

function loadSchedule() {
    const context = { console, window: {} };
    vm.createContext(context);
    vm.runInContext(`${source}\nthis.buildVisemeSchedule = buildVisemeSchedule;`, context);
    return context.buildVisemeSchedule;
}

test('prosody stretches pauses and raises mouth openness', () => {
    const schedule = loadSchedule();
    const text = 'Hello, world.';
    const plain = schedule(text);
    const measured = schedule(text, { pauseRatio: 0.8, pitchVariance: 0.9, rms: 0.5 });

    const silentTime = (s) => s.segments
        .filter((seg) => seg.viseme === null)
        .reduce((sum, seg) => sum + (seg.t1 - seg.t0), 0);
    assert.ok(silentTime(measured) > silentTime(plain), 'real pauses must lengthen silent segments');

    const peakOpen = (s) => Math.max(...s.segments.filter((seg) => seg.viseme !== null).map((seg) => seg.open));
    assert.ok(peakOpen(measured) >= peakOpen(plain), 'lively pitch must open the mouth at least as far');
});
