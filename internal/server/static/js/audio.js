// Audio playback with queue-based sequential playback and lip-sync hooks

class AudioPlayer {
    constructor() {
        this.audioContext = null;
        this.analyser = null;
        this.queue = [];
        this.playing = false;
        this.onSpeakingStart = null;
        this.onSpeakingEnd = null;
        this._currentSource = null;
        this._draining = false;

        // Playback clock for viseme-accurate lip sync
        this._utteranceStartCtx = null; // AudioContext.currentTime when current utterance began
        this._knownDuration = 0;        // cumulative seconds of queued audio for current utterance
    }

    init() {
        try {
            this.audioContext = new (window.AudioContext || window.webkitAudioContext)();
            this.analyser = this.audioContext.createAnalyser();
            this.analyser.fftSize = 512;
            this.analyser.smoothingTimeConstant = 0.7;
            this.analyser.connect(this.audioContext.destination);
        } catch (e) {
            console.warn('[Audio] Web Audio API not available:', e);
        }
    }

    resume() {
        if (this.audioContext && this.audioContext.state === 'suspended') {
            this.audioContext.resume();
        }
    }

    // Queue a chunk for sequential playback
    queueChunk(base64Audio, sampleRate, done) {
        if (!this.audioContext) this.init();
        this.resume();

        // Track cumulative duration of the current utterance (for viseme timing)
        const bytes = base64Audio ? atob(base64Audio).length : 0;
        const sr = sampleRate || 24000;
        this._knownDuration += (bytes / 2) / sr;

        this.queue.push({ base64Audio, sampleRate, done });
        if (!this._draining) {
            this._drainQueue();
        }
    }

    async _drainQueue() {
        if (this._draining) return;
        this._draining = true;

        // Signal speaking start
        if (!this.playing && this.queue.length > 0) {
            this.playing = true;
            // Mark utterance start time on the audio clock for viseme playhead
            if (this.audioContext) {
                this._utteranceStartCtx = this.audioContext.currentTime;
            }
            if (this.onSpeakingStart) this.onSpeakingStart();
        }

        while (this.queue.length > 0) {
            const chunk = this.queue.shift();
            try {
                await this._playRaw(chunk.base64Audio, chunk.sampleRate);
            } catch (e) {
                console.error('[Audio] Chunk play error:', e);
            }

            // If this was the final chunk and queue is empty, end speaking
            if (chunk.done && this.queue.length === 0) {
                break;
            }
        }

        this.playing = false;
        this._draining = false;
        this._utteranceStartCtx = null;
        if (this.onSpeakingEnd) this.onSpeakingEnd();
    }

    _playRaw(base64Audio, sampleRate) {
        return new Promise((resolve, reject) => {
            try {
                const binaryString = atob(base64Audio);
                const bytes = new Uint8Array(binaryString.length);
                for (let i = 0; i < binaryString.length; i++) {
                    bytes[i] = binaryString.charCodeAt(i);
                }

                // Convert int16 PCM to float32
                const int16Array = new Int16Array(bytes.buffer);
                const float32Array = new Float32Array(int16Array.length);
                for (let i = 0; i < int16Array.length; i++) {
                    float32Array[i] = int16Array[i] / 32768.0;
                }

                const audioBuffer = this.audioContext.createBuffer(1, float32Array.length, sampleRate || 24000);
                audioBuffer.getChannelData(0).set(float32Array);

                const source = this.audioContext.createBufferSource();
                source.buffer = audioBuffer;
                source.connect(this.analyser);
                this._currentSource = source;

                source.onended = () => {
                    this._currentSource = null;
                    resolve();
                };
                source.start();
            } catch (e) {
                reject(e);
            }
        });
    }

    // Legacy: direct play without queue (for backwards compat)
    async playChunk(base64Audio, sampleRate) {
        if (!this.audioContext) this.init();
        this.resume();

        try {
            const binaryString = atob(base64Audio);
            const bytes = new Uint8Array(binaryString.length);
            for (let i = 0; i < binaryString.length; i++) {
                bytes[i] = binaryString.charCodeAt(i);
            }

            // Convert int16 PCM to float32
            const int16Array = new Int16Array(bytes.buffer);
            const float32Array = new Float32Array(int16Array.length);
            for (let i = 0; i < int16Array.length; i++) {
                float32Array[i] = int16Array[i] / 32768.0;
            }

            const audioBuffer = this.audioContext.createBuffer(1, float32Array.length, sampleRate || 24000);
            audioBuffer.getChannelData(0).set(float32Array);

            const source = this.audioContext.createBufferSource();
            source.buffer = audioBuffer;
            source.connect(this.analyser);

            return new Promise((resolve) => {
                source.onended = resolve;
                source.start();
            });
        } catch (e) {
            console.error('[Audio] Play error:', e);
        }
    }

    getFrequencyData() {
        if (!this.analyser) return new Uint8Array(0);
        const data = new Uint8Array(this.analyser.frequencyBinCount);
        this.analyser.getByteFrequencyData(data);
        return data;
    }

    stop() {
        this.queue = [];
        if (this._currentSource) {
            try { this._currentSource.stop(); } catch (e) { /* already stopped */ }
            this._currentSource = null;
        }
        this.playing = false;
        this._draining = false;
        this._utteranceStartCtx = null;
        this._knownDuration = 0;
    }

    // ── Lip-sync playback clock ──
    // Seconds elapsed since the current utterance started playing.
    getPlayhead() {
        if (this._utteranceStartCtx === null || !this.audioContext) return 0;
        return Math.max(0, this.audioContext.currentTime - this._utteranceStartCtx);
    }

    // Cumulative seconds of audio queued for the current utterance.
    getKnownDuration() {
        return this._knownDuration;
    }
}
