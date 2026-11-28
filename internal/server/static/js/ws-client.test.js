// Binary-frame dispatch and legacy compat tests for the shared WS client.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('./ws-client.js', import.meta.url), 'utf8');

function loadWSClient(token = '') {
    class FakeWebSocket {
        constructor(url) {
            this.url = url;
            this.readyState = 1;
            this.sent = [];
            FakeWebSocket.instances.push(this);
        }
        send(data) { this.sent.push(data); }
        close() {}
    }
    FakeWebSocket.OPEN = 1;
    FakeWebSocket.CONNECTING = 0;
    FakeWebSocket.instances = [];

    let btoaCalls = 0;
    const context = {
        WebSocket: FakeWebSocket,
        generateId: () => 'id-1',
        console,
        setTimeout,
        btoa: (s) => { btoaCalls++; return Buffer.from(s, 'binary').toString('base64'); },
    };
    vm.createContext(context);
    vm.runInContext(`${source}\nthis.WSClient = WSClient;`, context);
    const client = new context.WSClient('ws://localhost/ws', token);
    client.connect();
    return { client, socket: FakeWebSocket.instances[0], context, btoaCalls: () => btoaCalls };
}

test('auth token is appended to the websocket URL', () => {
    const { socket } = loadWSClient('a b');
    assert.equal(socket.url, 'ws://localhost/ws?token=a%20b');
});

// Build the frame inside the VM so `instanceof ArrayBuffer` matches the client.
function ttsFrame(context, { rate, flags, duration, samples }) {
    return vm.runInContext(`(() => {
        const buf = new ArrayBuffer(14 + ${samples.length * 2});
        const view = new DataView(buf);
        view.setUint8(0, 0x54);
        view.setUint32(1, ${rate}, true);
        view.setUint8(5, ${flags});
        view.setFloat64(6, ${duration}, true);
        new Int16Array(buf, 14).set([${samples.join(',')}]);
        return buf;
    })()`, context);
}

test('binary TTS frames dispatch with PCM and legacy base64 audio', () => {
    const { client, socket, context, btoaCalls } = loadWSClient();
    assert.equal(socket.binaryType, 'arraybuffer');

    let params;
    client.on('tts.chunk', p => { params = p; });
    socket.onmessage({ data: ttsFrame(context, { rate: 16000, flags: 0x01, duration: 1.5, samples: [0, 16384, -16384] }) });

    assert.equal(params.sample_rate, 16000);
    assert.equal(params.done, true);
    assert.equal(params.muted, false);
    assert.equal(params.duration_seconds, 1.5);
    assert.deepEqual([...params.pcm], [0, 16384, -16384]);
    assert.equal(btoaCalls(), 0, 'base64 is lazy: binary-only consumers do not pay for it');
    assert.equal(params.audio, Buffer.from(new Int16Array([0, 16384, -16384]).buffer).toString('base64'));
    assert.equal(btoaCalls(), 1);
});

test('JSON notifications and binary sends still work', () => {
    const { client, socket, context } = loadWSClient();
    let status;
    client.on('status.changed', p => { status = p.status; });
    socket.onmessage({ data: '{"method":"status.changed","params":{"status":"thinking"}}' });
    assert.equal(status, 'thinking');

    const frame = vm.runInContext('new ArrayBuffer(5)', context);
    assert.equal(client.sendBinary(frame), true);
    assert.deepEqual(socket.sent, [frame]);
});
