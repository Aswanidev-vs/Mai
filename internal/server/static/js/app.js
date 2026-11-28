// Main application entry point

const wsProtocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
const wsUrl = `${wsProtocol}//${location.host}/ws`;

// Auth token comes from the page URL once, then persists locally. The server
// wraps every route when server.token is set, so the WS upgrade needs it too.
const urlToken = new URLSearchParams(location.search).get('token');
if (urlToken) localStorage.setItem('mai-ws-token', urlToken);
const wsToken = urlToken || localStorage.getItem('mai-ws-token') || '';

const ws = new WSClient(wsUrl, wsToken);
const chat = new ChatUI();
const audio = new AudioPlayer();
const character = new CharacterRenderer('characterContainer');
const settings = new SettingsPanel(ws);

const qualitySelect = document.getElementById('settingQuality');
if (qualitySelect) {
    qualitySelect.value = character.quality;
    qualitySelect.addEventListener('change', () => character.setQuality(qualitySelect.value));
}

// Emotion comes from the backend (emotion.mai / emotion.detected); turn state
// comes from companion.phase. The client no longer guesses either from text.
const USER_MOOD_TO_RESPONSE = {
    sad: 'sad', stressed: 'calm', frustrated: 'calm',
    excited: 'happy', happy: 'happy', calm: 'calm', neutral: 'calm',
};
let currentPhase = 'idle';


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
audio.onChunkScheduled = (duration) => {
    character.setVisemeDuration(duration);
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
let gazeAvoidBuffer = '';

// Uncertainty markers that trigger gaze avoidance (embarrassment/shyness)
const UNCERTAINTY_MARKERS = [
    "i'm not sure",
    "i might be wrong",
    "i don't know",
    "i'm not certain",
    "i could be wrong",
    "not sure",
    "maybe i'm wrong",
    "i think",
    "probably",
    "perhaps",
    "i guess",
    "i suppose",
    "it depends",
    "hard to say",
    "unclear",
    "uncertain",
    "i'm unsure",
    "i'm uncertain",
];

ws.on('chat.response', (params) => {
    if (params.text) {
        if (!streamingActive) {
            chat.startAgentMessage();
            streamingActive = true;
            ttsTextBuffer = '';
            gazeAvoidBuffer = '';
        }
        chat.streamToken(params.text);
        ttsTextBuffer += params.text;
        gazeAvoidBuffer += params.text;
        // Rebuild the viseme schedule as spoken sentences arrive, so it is
        // ready before/during audio playback instead of only at stream end.
        character.prepareVisemes(params.text);
        // Queue the reset now; AudioPlayer applies it when this sentence's
        // first PCM is actually scheduled on the AudioContext timeline.
        audio.beginVisemeUtterance();
    }
    if (params.done) {
        chat.finalizeMessage();
        streamingActive = false;
        // The send button is only disabled in _send(); re-enable it here so
        // the user can queue a follow-up while Mai is still speaking (the
        // transcript itself stays open until her voice ends).
        if (chat.sendBtn) chat.sendBtn.disabled = false;
        // Let the expression gently decay back toward neutral rather than snapping.
        // Check for uncertainty markers and trigger gaze avoidance
        const lower = gazeAvoidBuffer.toLowerCase();
        for (const marker of UNCERTAINTY_MARKERS) {
            if (lower.includes(marker)) {
                character.setGazeAvoidTrigger();
                break;
            }
        }
        ttsTextBuffer = '';
        gazeAvoidBuffer = '';
    }
});

// Turn choreography: one backend phase drives posture, gaze and attention.
ws.on('companion.phase', (params) => {
    const phase = params.phase || 'idle';
    if (phase === currentPhase) return;
    currentPhase = phase;
    character.setPhase(phase);
    character.setUserSpeaking(phase === 'listening' || phase === 'wake');
    if (phase === 'wake') showCaption('I'm here', true);
});

const asrCaption = document.getElementById('asrCaption');
let asrCaptionTimer = null;
function showCaption(text, final) {
    if (!asrCaption) return;
    asrCaption.textContent = text || '';
    asrCaption.classList.add('visible');
    clearTimeout(asrCaptionTimer);
    if (final) asrCaptionTimer = setTimeout(() => asrCaption.classList.remove('visible'), 2200);
}

ws.on('asr.partial', (params) => {
    character.setUserSpeaking(true);
    showCaption(params.text, false);
});
ws.on('asr.final', (params) => {
    character.setUserSpeaking(false);
    showCaption(params.text, true);
});

// A reconnecting client resumes the same moment rather than snapping to neutral.
ws.on('companion.presence', (params) => {
    if (params.emotion) character.setEmotion(params.emotion, params.intensity || 0.5);
    if (params.phase && params.phase !== currentPhase) {
        currentPhase = params.phase;
        character.setPhase(params.phase);
    }
});

// Measured prosody reshapes the viseme timeline for the next sentence.
ws.on('companion.prosody', (params) => character.setProsody(params));

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

// TTS audio chunks — queue for sequential playback with lip sync.
// Binary frames expose `pcm` (an Int16Array) and avoid base64 entirely; the
// legacy `audio` field remains for clients that still receive JSON.
ws.on('tts.chunk', (params) => {
    const pcm = params.pcm;
    // Check pcm first so the legacy base64 getter is not evaluated for binary frames.
    if (!(pcm && pcm.length) && !params.audio && !params.done) return;
    // Ensure analyser is connected before first chunk
    if (audio.analyser && !character.analyser) {
        character.setAnalyser(audio.analyser);
    }
    audio.queueChunk(
        pcm && pcm.length ? pcm : (params.audio || ''),
        params.sample_rate,
        !!params.done,
        !!params.muted,
        Number(params.duration_seconds) || 0
    );
});

// Emotion detection
ws.on('emotion.detected', (params) => {
    const emotion = USER_MOOD_TO_RESPONSE[params.emotion] || 'calm';
    const intensity = Math.min(0.55, Math.max(0.22, Number(params.intensity) || 0.35));
    character.setEmotion(emotion, intensity);

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

// Mai's own sentiment (published by the orchestrator from her response text).
// This drives her expression from what SHE is feeling — independent of and
// complementary to the user's mood. Backend labels (happy, sad, excited,
// frustrated, stressed, calm, neutral) all map 1:1 to EMOTION_MAP keys.
ws.on('emotion.mai', (params) => {
    const emotion = params.emotion || 'neutral';
    const intensity = Math.min(0.7, Math.max(0.2, Number(params.intensity) || 0.4));
    character.setEmotion(emotion, intensity);
});

ws.on('config.changed', (params) => {
    console.log('[Config]', params.key, '=', params.value);
});

// Dance request from the backend — fires for both voice and chat turns
// (intent detection happens in the orchestrator's HandleInput).
ws.on('companion.dance', () => {
    character.dance();
});

// Explicit action request from backend (motion + expression together)
ws.on('companion.action', (params) => {
    if (!params || !params.action) return;
    console.log('[Action] Triggered:', params.action, params.duration);
    if (typeof character.performAction === 'function') {
        character.performAction(params.action, params.duration);
    } else if (params.action === 'dance' && typeof character.dance === 'function') {
        character.dance(params.duration);
    }
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
    node: null,
    active: false,

    // Binary mic frame: 1-byte tag + uint32 LE sample rate + int16 LE PCM.
    _sendPCM(float32) {
        const buf = new ArrayBuffer(5 + float32.length * 2);
        const view = new DataView(buf);
        view.setUint8(0, 0x41);
        view.setUint32(1, 16000, true);
        for (let i = 0; i < float32.length; i++) {
            const s = Math.max(-1, Math.min(1, float32[i]));
            view.setInt16(5 + i * 2, s < 0 ? s * 0x8000 : s * 0x7fff, true);
        }
        ws.sendBinary(buf);
    },

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

        // AudioWorklet keeps capture off the main thread and cuts latency from
        // ScriptProcessor's 2048 frames (~128 ms) to 256 (~16 ms).
        if (this.ctx.audioWorklet) {
            try {
                await this.ctx.audioWorklet.addModule('/js/pcm-capture-processor.js');
                this.node = new AudioWorkletNode(this.ctx, 'pcm-capture');
                this.node.port.onmessage = (ev) => this._sendPCM(ev.data);
            } catch (e) {
                console.warn('[MIC] AudioWorklet unavailable, falling back:', e);
            }
        }
        if (!this.node) {
            this.node = this.ctx.createScriptProcessor(256, 1, 1);
            this.node.onaudioprocess = (ev) => this._sendPCM(new Float32Array(ev.inputBuffer.getChannelData(0)));
        }

        const source = this.ctx.createMediaStreamSource(this.stream);
        const silent = this.ctx.createGain();
        silent.gain.value = 0;
        source.connect(this.node);
        // Worklet nodes are pull-driven: keep a (silent) path to the destination.
        this.node.connect(silent);
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
        if (this.node) { try { this.node.disconnect(); } catch (e) {} this.node = null; }
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
