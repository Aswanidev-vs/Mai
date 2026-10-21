import test from 'node:test';
import assert from 'node:assert/strict';
import { selectWalkClipKey } from './walk-direction.js';

test('horizontal movement uses the profile clips', () => {
    assert.equal(selectWalkClipKey(-120, 0), 'walk-left-profile');
    assert.equal(selectWalkClipKey(120, 0), 'walk-right-profile');
    assert.equal(selectWalkClipKey(-120, 20), 'walk-left-profile');
    assert.equal(selectWalkClipKey(120, -20), 'walk-right-profile');
});

test('diagonal movement uses the three-quarter clips', () => {
    assert.equal(selectWalkClipKey(-80, 80), 'walk-three-quarter-left');
    assert.equal(selectWalkClipKey(80, 80), 'walk-three-quarter-right');
    assert.equal(selectWalkClipKey(-80, -80), 'walk-three-quarter-left');
    assert.equal(selectWalkClipKey(80, -80), 'walk-three-quarter-right');
});

test('mostly vertical movement falls back to the front clip', () => {
    assert.equal(selectWalkClipKey(0, 120), 'walk-front');
    assert.equal(selectWalkClipKey(0, -120), 'walk-front');
    assert.equal(selectWalkClipKey(20, 120), 'walk-front');
});

test('zero movement is a safe front-clip default', () => {
    assert.equal(selectWalkClipKey(0, 0), 'walk-front');
    assert.equal(selectWalkClipKey(undefined, undefined), 'walk-front');
});
