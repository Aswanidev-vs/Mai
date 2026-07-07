// Main application entry point

const wsProtocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
const wsUrl = `${wsProtocol}//${location.host}/ws`;

const ws = new WSClient(wsUrl);
const chat = new ChatUI();
const audio = new AudioPlayer();
const character = new CharacterRenderer('characterContainer');
const settings = new SettingsPanel(ws);

// Wire audio analyser to character for lip sync
audio.init();
if (audio.analyser) {
    character.setAnalyser(audio.analyser);
}
character.setAudioPlayer(audio);

// Wire speaking lifecycle to character
audio.onSpeakingStart = () => {
    character.setSpeaking(true);
};
audio.onSpeakingEnd = () => {
    character.setSpeaking(false);
};

const statusIndicator = document.getElementById('statusIndicator');
const statusText = document.getElementById('statusText');
const emotionBadge = document.getElementById('emotionBadge');
const emotionIcon = document.getElementById('emotionIcon');
const emotionLabel = document.getElementById('emotionLabel');

// Wire chat input to WS
chat.onSend = (text) => {
    ws.send('chat.input', { text });
};

// WS event handlers
ws.onConnect = () => {
    statusIndicator.className = 'status-indicator connected';
    statusText.textContent = 'Connected';
    chat.addSystemMessage('Connected to Mai');
};

ws.onDisconnect = () => {
    statusIndicator.className = 'status-indicator';
    statusText.textContent = 'Reconnecting...';
};

// Chat response streaming
let streamingActive = false;
let ttsTextBuffer = '';

ws.on('chat.response', (params) => {
    if (params.text) {
        if (!streamingActive) {
            chat.startAgentMessage();
            streamingActive = true;
            ttsTextBuffer = '';
        }
        chat.streamToken(params.text);
        // Accumulate the spoken text to drive word-accurate lip sync
        ttsTextBuffer += params.text;
    }
    if (params.done) {
        chat.finalizeMessage();
        streamingActive = false;
        // Build the viseme timeline from the full utterance Mai is about to speak
        if (ttsTextBuffer.trim().length > 0) {
            character.prepareVisemes(ttsTextBuffer);
        }
        ttsTextBuffer = '';
    }
});

// Status changes
ws.on('status.changed', (params) => {
    const status = params.status || 'idle';
    statusText.textContent = status.charAt(0).toUpperCase() + status.slice(1);
    character.setStatus(status);

    statusIndicator.className = 'status-indicator connected';
    if (status === 'thinking') {
        statusIndicator.classList.add('thinking');
    } else if (status === 'speaking') {
        statusIndicator.classList.add('speaking');
        // Speaking state is managed by audio callbacks, not status events
    }
});

// TTS audio chunks — queue for sequential playback with lip sync
ws.on('tts.chunk', (params) => {
    if (params.audio) {
        // Ensure analyser is connected before first chunk
        if (audio.analyser && !character.analyser) {
            character.setAnalyser(audio.analyser);
        }
        audio.queueChunk(params.audio, params.sample_rate, !!params.done);
        // Feed the running audio duration so the viseme schedule stays scaled to reality
        character.setVisemeDuration(audio.getKnownDuration());
    }
});

// Emotion detection
ws.on('emotion.detected', (params) => {
    character.setEmotion(params.emotion, params.intensity);

    const emotionNames = {
        calm: 'Calm', happy: 'Happy', sad: 'Sad',
        stressed: 'Stressed', excited: 'Excited', frustrated: 'Frustrated',
    };
    const emotionIcons = {
        calm: '', happy: '', sad: '',
        stressed: '', excited: '', frustrated: '',
    };

    emotionIcon.textContent = emotionIcons[params.emotion] || '';
    emotionLabel.textContent = emotionNames[params.emotion] || params.emotion;
    emotionBadge.classList.add('active');
});

ws.on('config.changed', (params) => {
    console.log('[Config]', params.key, '=', params.value);
});

ws.on('state.request', (params) => {
    statusText.textContent = params.status || 'idle';
});

// ── React to user typing / presence (makes her feel attentive) ──
const chatInputEl = document.getElementById('chatInput');
if (chatInputEl) {
    const onType = () => character.setUserTyping(chatInputEl.value.length > 0);
    chatInputEl.addEventListener('input', onType);
    chatInputEl.addEventListener('focus', () => character.setUserTyping(chatInputEl.value.length > 0));
    chatInputEl.addEventListener('blur', () => character.setUserTyping(false));
}
// Any interaction with the page makes her "notice" you and wake from rest.
let presenceThrottle = 0;
const onPresence = () => {
    const now = performance.now();
    if (now - presenceThrottle < 1500) return;
    presenceThrottle = now;
    character.notifyUserPresent();
};
document.addEventListener('mousemove', onPresence);
document.addEventListener('keydown', onPresence);
document.addEventListener('click', onPresence);

// ── Voice input: talk to Mai with your microphone (real-time) ──
const micBtn = document.getElementById('micToggle');
const micController = {
    stream: null,
    ctx: null,
    proc: null,
    active: false,

    async start() {
        if (this.active) return;
        try {
            this.stream = await navigator.mediaDevices.getUserMedia({
                audio: { sampleRate: 16000, channelCount: 1, echoCancellation: true, noiseSuppression: true }
            });
        } catch (e) {
            console.error('[MIC] Microphone access denied:', e);
            return;
        }
        const Ctx = window.AudioContext || window.webkitAudioContext;
        this.ctx = new Ctx({ sampleRate: 16000 });
        if (this.ctx.state === 'suspended') await this.ctx.resume();

        const source = this.ctx.createMediaStreamSource(this.stream);
        // ScriptProcessor gives us raw PCM frames to stream to the backend ASR.
        this.proc = this.ctx.createScriptProcessor(2048, 1, 1);
        this.proc.onaudioprocess = (ev) => {
            const input = ev.inputBuffer.getChannelData(0);
            const len = input.length;
            const buf = new ArrayBuffer(len * 2);
            const view = new DataView(buf);
            for (let i = 0; i < len; i++) {
                let s = Math.max(-1, Math.min(1, input[i]));
                view.setInt16(i * 2, s < 0 ? s * 0x8000 : s * 0x7fff, true);
            }
            const b64 = btoa(String.fromCharCode.apply(null, new Uint8Array(buf)));
            ws.send('audio.input', { audio: b64, sample_rate: 16000 });
        };
        const silent = this.ctx.createGain();
        silent.gain.value = 0;
        source.connect(this.proc);
        this.proc.connect(silent);
        silent.connect(this.ctx.destination);

        this.active = true;
        if (micBtn) {
            micBtn.classList.add('active');
            micBtn.title = 'Stop listening';
        }
        statusText.textContent = 'Listening…';
        ws.send('audio.input.start', {});
        character.notifyUserPresent();
    },

    stop() {
        if (!this.active) return;
        this.active = false;
        if (this.stream) { this.stream.getTracks().forEach(t => t.stop()); this.stream = null; }
        if (this.proc) { try { this.proc.disconnect(); } catch (e) {} this.proc = null; }
        if (this.ctx) { this.ctx.close(); this.ctx = null; }
        if (micBtn) {
            micBtn.classList.remove('active');
            micBtn.title = 'Talk to Mai';
        }
        ws.send('audio.input.stop', {});
    },

    toggle() { this.active ? this.stop() : this.start(); }
};
if (micBtn) micBtn.addEventListener('click', () => micController.toggle());

ws.connect();
document.getElementById('chatInput').focus();
