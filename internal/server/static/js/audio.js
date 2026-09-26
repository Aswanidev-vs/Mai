// Audio playback with overlap-add smooth streaming and lip-sync hooks

class AudioPlayer {
    constructor() {
        this.audioContext = null;
        this.analyser = null;
        this.queue = [];
        this.playing = false;
        this.onSpeakingStart = null;
        this.onSpeakingEnd = null;
        this.onChunkScheduled = null;
        this._draining = false;
        this._generation = 0;
        this._visemeSentencePending = false;

        // Playback clock for viseme-accurate lip sync
        this._utteranceStartCtx = null;
        this._knownDuration = 0;

        // Overlap-add scheduling
        this._nextStartTime = 0;       // AudioContext time when next chunk should start
        this._crossfadeMs = 40;        // 40ms crossfade between chunks
        this._activeSources = [];      // Track active sources for cleanup
    }

    init() {
        try {
            this.audioContext = new (window.AudioContext || window.webkitAudioContext)();
            this.analyser = this.audioContext.createAnalyser();
            this.analyser.fftSize = 512;
            this.analyser.smoothingTimeConstant = 0.7;
            // NOTE: deliberately NOT connected to the destination. The
            // analyser is a pass-through node, so wiring it here used to make
            // everything fed to it audible on its own — which blocked muted
            // (lip-sync-only) audio mirroring. Audible output is routed
            // through the playback gain nodes in _playSmooth instead.
            // Resume on any user gesture (click, key, touch)
            const resumeCtx = () => {
                if (this.audioContext && this.audioContext.state === 'suspended') {
                    this.audioContext.resume();
                }
            };
            for (const evt of ['click', 'keydown', 'touchstart']) {
                document.addEventListener(evt, resumeCtx, { once: false, passive: true });
            }
        } catch (e) {
            console.warn('[Audio] Web Audio API not available:', e);
        }
    }

    async resume() {
        if (this.audioContext && this.audioContext.state === 'suspended') {
            try {
                await this.audioContext.resume();
            } catch (e) {
                console.warn('[Audio] Failed to resume AudioContext:', e);
            }
        }
    }

    // Queue a chunk for smooth sequential playback. `muted` marks mirrored
    // audio from local-speaker mode: it is scheduled and analysed exactly like
    // audible audio, but output at zero gain so the tab never doubles the
    // sound while its lip-sync clock still follows the real voice.
    // Queue in arrival order before resuming. Resuming first yields to the
    // event loop, allowing bursty WebSocket chunks to enter the queue out of order.
    queueChunk(base64Audio, sampleRate, done, muted, durationSeconds = 0) {
        if (!this.audioContext) this.init();
        this.queue.push({ base64Audio, sampleRate, done, muted: !!muted, durationSeconds: Number(durationSeconds) || 0 });
        if (!this._draining) {
            this._drainQueue();
        }
        this.resume();
    }

    async _drainQueue() {
        if (this._draining) return;
        this._draining = true;
        const generation = this._generation;
        let chunkIndex = 0;
        let emptySince = performance.now();

        // Signal speaking start
        if (!this.playing && this.queue.length > 0) {
            this.playing = true;
            if (this.audioContext) {
                this._utteranceStartCtx = this.audioContext.currentTime;
                this._nextStartTime = this.audioContext.currentTime;
            }
            // New utterance: restart the viseme clock so getKnownDuration()
            // matches only what this utterance will play (it used to grow
            // forever across turns, desyncing lip sync after the first reply).
            this._knownDuration = 0;
            if (this.onSpeakingStart) this.onSpeakingStart();
        }

        // `done` is the only end-of-turn signal. WebSocket chunks can arrive
        // with real-time gaps while the next sentence is synthesized, so an
        // empty queue must not close the mouth between chunks.
        while (generation === this._generation) {
            if (this.queue.length === 0) {
                // Bound only the gap between chunks. Normal TTS gaps are short;
                // a dropped/lost done marker must not leave the renderer speaking.
                if (performance.now() - emptySince > 5000) break;
                await new Promise(r => setTimeout(r, 10));
                continue;
            }
            const chunk = this.queue.shift();
            try {
                // Sources are played sequentially, so the next chunk starts
                // after this one ends. Gain automation still protects boundaries.
                await this._playSmooth(chunk.base64Audio, chunk.sampleRate, chunkIndex > 0, chunk.muted, chunk.durationSeconds);
            } catch (e) {
                console.error('[Audio] Chunk play error:', e);
            }
            chunkIndex++;
            emptySince = performance.now();
            if (chunk.done) break;
        }

        if (generation === this._generation) {
            this.playing = false;
            this._draining = false;
            this._utteranceStartCtx = null;
            this._nextStartTime = 0;
            if (this.onSpeakingEnd) this.onSpeakingEnd();
        }
    }

    // Smooth overlap-add playback — schedules chunk on AudioContext timeline
    // with crossfade gain to eliminate gaps between chunks. With `muted`, the
    // chunk is analysed but silent: local speakers own the sound, the tab only
    // mirrors the timeline for lip sync.
    _playSmooth(base64Audio, sampleRate, applyCrossfade, muted, durationSeconds = 0) {
        return new Promise((resolve, reject) => {
            try {
                const binaryString = atob(base64Audio);
                // Zero-length chunk = done marker; resolve immediately.
                if (binaryString.length === 0) {
                    resolve();
                    return;
                }
                const bytes = new Uint8Array(binaryString.length);
                for (let i = 0; i < binaryString.length; i++) {
                    bytes[i] = binaryString.charCodeAt(i);
                }

                const int16Array = new Int16Array(bytes.buffer);
                const float32Array = new Float32Array(int16Array.length);
                for (let i = 0; i < int16Array.length; i++) {
                    float32Array[i] = int16Array[i] / 32768.0;
                }

                const sr = sampleRate || 24000;
                const audioBuffer = this.audioContext.createBuffer(1, float32Array.length, sr);
                audioBuffer.getChannelData(0).set(float32Array);
                const source = this.audioContext.createBufferSource();
                source.buffer = audioBuffer;

                // Gain node for crossfade (and the mute switch)
                const gainNode = this.audioContext.createGain();
                source.connect(gainNode);

                // Analyse the source before the mute gain so local-speaker mirrors
                // still drive the mouth. Only the destination path is muted.
                source.connect(this.analyser);
                gainNode.connect(this.audioContext.destination);
                gainNode.gain.value = muted ? 0 : 1;

                // Schedule start time: either now or at the previously planned end
                const now = this.audioContext.currentTime;
                const startTime = Math.max(this._nextStartTime, now);
                const chunkDuration = float32Array.length / sr;
                const crossfade = this._crossfadeMs / 1000;

                // A transcript sentence boundary resets the viseme phase, but its
                // clock can start only when that sentence's first PCM is scheduled.
                // Network latency between the two notifications must not accumulate.
                if (this._visemeSentencePending) {
                    this._visemeSentencePending = false;
                    this._utteranceStartCtx = startTime;
                    this._knownDuration = 0;
                }
                // The backend repeats the complete sentence duration on every PCM
                // chunk. Prefer it over a running sum so the first chunk already
                // maps the full text schedule to the full audio timeline.
                this._knownDuration = durationSeconds > 0 ? durationSeconds : this._knownDuration + chunkDuration;
                if (this.onChunkScheduled) this.onChunkScheduled(this._knownDuration);

                if (muted) {
                    // Silent mirror: keep the exact chunk timing (viseme
                    // positions depend on it) but skip the gain automation.
                    gainNode.gain.setValueAtTime(0, startTime);
                    this._nextStartTime = startTime + chunkDuration - (applyCrossfade ? crossfade : 0);
                } else if (applyCrossfade) {
                    // Crossfade in (first 40ms of this chunk)
                    gainNode.gain.setValueAtTime(0, startTime);
                    gainNode.gain.linearRampToValueAtTime(1, startTime + crossfade);

                    // Crossfade out (last 40ms of this chunk) — only if more chunks coming
                    if (this.queue.length > 0) {
                        const fadeOutStart = startTime + chunkDuration - crossfade;
                        gainNode.gain.setValueAtTime(1, fadeOutStart);
                        gainNode.gain.linearRampToValueAtTime(0, startTime + chunkDuration);
                    }

                    // Schedule the next chunk to start slightly before this one ends
                    this._nextStartTime = startTime + chunkDuration - crossfade;
                } else {
                    // First chunk of utterance: play at full volume, no fade-in
                    gainNode.gain.setValueAtTime(1, startTime);
                    this._nextStartTime = startTime + chunkDuration;
                }

                source.start(startTime);

                // Track for cleanup
                const srcInfo = { source, gainNode };
                this._activeSources.push(srcInfo);

                source.onended = () => {
                    // Clean up
                    const idx = this._activeSources.indexOf(srcInfo);
                    if (idx >= 0) this._activeSources.splice(idx, 1);
                    try { gainNode.disconnect(); } catch (e) { /* already disconnected */ }
                    resolve();
                };
            } catch (e) {
                reject(e);
            }
        });
    }

    async _waitForLastSource() {
        // Wait up to 500ms for the last active source to finish
        const deadline = performance.now() + 500;
        while (this._activeSources.length > 0 && performance.now() < deadline) {
            await new Promise(r => setTimeout(r, 10));
        }
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

            const int16Array = new Int16Array(bytes.buffer);
            const float32Array = new Float32Array(int16Array.length);
            for (let i = 0; i < int16Array.length; i++) {
                float32Array[i] = int16Array[i] / 32768.0;
            }

            const audioBuffer = this.audioContext.createBuffer(1, float32Array.length, sampleRate || 24000);
            audioBuffer.getChannelData(0).set(float32Array);

            const source = this.audioContext.createBufferSource();
            source.buffer = audioBuffer;
            // Analyser alone is silent now; audible legacy playback routes
            // through the destination explicitly.
            source.connect(this.analyser);
            source.connect(this.audioContext.destination);

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
        this._generation++;
        this.queue = [];
        // Stop all active sources
        for (const src of this._activeSources) {
            try { src.source.stop(); } catch (e) { /* already stopped */ }
            try { src.gainNode.disconnect(); } catch (e) { /* already disconnected */ }
        }
        this._activeSources = [];
        this.playing = false;
        this._draining = false;
        this._utteranceStartCtx = null;
        this._knownDuration = 0;
        this._nextStartTime = 0;
        this._visemeSentencePending = false;
        if (this.onSpeakingEnd) this.onSpeakingEnd();
    }

    // Begin a new sentence on the existing turn's audio timeline. The backend
    // announces each sentence immediately before its PCM, so the text-derived
    // schedule must be normalized to that sentence rather than the whole reply.
    beginVisemeUtterance() {
        this._visemeSentencePending = true;
    }

    // ── Lip-sync playback clock ──
    getPlayhead() {
        if (this._utteranceStartCtx === null || !this.audioContext) return 0;
        return Math.max(0, this.audioContext.currentTime - this._utteranceStartCtx);
    }

    getKnownDuration() {
        return this._knownDuration;
    }
}
