// A syntax error in any served script aborts app.js before ws.connect(), which
// leaves the UI stuck at "Connecting..." with no obvious cause. Check them all.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../../../../', import.meta.url));
const directories = ['internal/server/static/js', 'transparent/web/js'];

test('every served JavaScript file parses', () => {
    for (const dir of directories) {
        const full = path.join(root, dir);
        for (const name of fs.readdirSync(full).filter((n) => n.endsWith('.js'))) {
            const file = path.join(full, name);
            try {
                execFileSync(process.execPath, ['--check', file], { stdio: 'pipe' });
            } catch (err) {
                assert.fail(`${dir}/${name} has a syntax error:\n${err.stderr.toString()}`);
            }
        }
    }
});
