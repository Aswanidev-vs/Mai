// JS unit tests for Web Audio playback and lip-sync scheduling.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('./audio.js', import.meta.url), 'utf8');

function loadAudioPlayer() {
    class FakeParam {
        constructor() { this.value = 0; this.events = []; }
        setValueAtTime(value, time) { this.value = value; this.events.push(['set', value, time]); }
        linearRampToValueAtTime(value, time) { this.value = value; this.events.push(['ramp', value, time]); }
    }
    class FakeNode {
        constructor() { this.connections = []; this.gain = new FakeParam(); }
        connect(node) { this.connections.push(node); }
        disconnect() { this.disconnected = true; }
    }
    class FakeSource extends FakeNode {
        constructor(context) { super(); this.context = context; this.onended = null; }
        start(time = 0) {
            this.startTime = time;
            this.context.currentTime = time;
            queueMicrotask(() => this.onended?.());
        }
        stop() {}
    }
    class FakeContext {
        constructor() {
            this.currentTime = 0;
            this.state = 'running';
            this.destination = new FakeNode();
            this.sources = [];
        }
        createAnalyser() { this.analyser = new FakeNode(); this.analyser.fftSize = 512; return this.analyser; }
        createGain() { return new FakeNode(); }
        createBuffer(channels, length, sampleRate) {
            return { length, sampleRate, data: new Float32Array(length), getChannelData: () => new Float32Array(length) };
        }
        createBufferSource() { const source = new FakeSource(this); this.sources.push(source); return source; }
        resume() { this.state = 'running'; return Promise.resolve(); }
    }
    const context = { window: { AudioContext: FakeContext }, document: { addEventListener() {} }, performance, setTimeout, console, atob };
    vm.createContext(context);
    vm.runInContext(`${source}\nthis.AudioPlayer = AudioPlayer;`, context);
    const player = new context.AudioPlayer();
    player.audioContext = new FakeContext();
    player.analyser = player.audioContext.createAnalyser();
    return { player, FakeContext };
}

function pcmBase64(sampleCount) {
    return Buffer.alloc(sampleCount * 2).toString('base64');
}

test('audible chunks reach analyser and destination; muted chunks stay zero gain', () => {
    const { player } = loadAudioPlayer();
    return Promise.all([
        player._playSmooth(pcmBase64(441), 44100, false, false),
        player._playSmooth(pcmBase64(441), 44100, false, true),
    ]).then(() => {
        const [audible, muted] = player.audioContext.sources;
        const [audibleGain, mutedGain] = [audible.connections[0], muted.connections[0]];
        assert.ok(audible.connections.includes(player.analyser));
        assert.ok(muted.connections.includes(player.analyser));
        assert.ok(audibleGain.connections.includes(player.audioContext.destination));
        assert.ok(mutedGain.connections.includes(player.audioContext.destination));
        assert.equal(audibleGain.gain.value, 1);
        assert.equal(mutedGain.gain.value, 0);
    });
});

test('sentence duration is applied on its first scheduled chunk', async () => {
    const { player } = loadAudioPlayer();
    player.beginVisemeUtterance();
    const durations = [];
    player.onChunkScheduled = duration => durations.push(duration);
    await player._playSmooth(pcmBase64(2205), 44100, false, true, 2.5);
    assert.deepEqual(durations, [2.5]);
    assert.equal(player.getKnownDuration(), 2.5);
    assert.equal(player.getPlayhead(), 0);
});

test('done marker ends speaking and does not require audio', async () => {
    const { player } = loadAudioPlayer();
    let started = 0;
    let ended = 0;
    player.onSpeakingStart = () => started++;
    player.onSpeakingEnd = () => ended++;
    player.queueChunk('', 44100, true, false, 0);
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.equal(started, 1);
    assert.equal(ended, 1);
    assert.equal(player.playing, false);
});
