// The status watchdog must be independent of the module scripts it guards.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('./watchdog.js', import.meta.url), 'utf8');

function run(initialText) {
    let fire;
    const logs = [];
    const status = { textContent: initialText };
    const badge = { className: 'status-indicator' };
    const elements = { statusText: status, statusIndicator: badge };
    const context = {
        window: {},
        document: { getElementById: (id) => elements[id] || null },
        location: { protocol: 'http:', host: 'localhost:8080' },
        console: { error: (...args) => logs.push(args.join(' ')) },
        setTimeout: (fn, ms) => { fire = fn; assert.equal(ms, 5000); },
    };
    vm.createContext(context);
    vm.runInContext(source, context);
    context.window.installMaiWatchdog(5000);
    fire();
    return { status, badge, logs };
}

test('a stuck Connecting label becomes an actionable offline message', () => {
    const { status, badge, logs } = run('Connecting...');
    assert.equal(status.textContent, 'Offline — check server');
    assert.equal(badge.className, 'status-indicator offline');
    assert.match(logs[0], /ws:\/\/localhost:8080\/ws/);
    assert.match(logs[0], /mai\.exe --companion/);
});

test('a valid connection state is left alone', () => {
    const { status, badge, logs } = run('Connected');
    assert.equal(status.textContent, 'Connected');
    assert.equal(badge.className, 'status-indicator');
    assert.equal(logs.length, 0);
});
