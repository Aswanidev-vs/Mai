// Main application entry point

const wsProtocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
const wsUrl = `${wsProtocol}//${location.host}/ws`;

const ws = new WSClient(wsUrl);
const chat = new ChatUI();
const audio = new AudioPlayer();
const character = new CharacterRenderer('characterContainer');
const settings = new SettingsPanel(ws);

// Small, deterministic cues keep the avatar responsive without asking the
// language model for brittle JSON or exposing internal emotion labels to the
// user. These are intentionally conservative: a technical answer should not
// make Mai look angry just because it mentions an error.
const ASSISTANT_EXPRESSION_CUES = [
    { pattern: /\b(heh|as expected|aren't you|you really are|don't flatter yourself|making things complicated|architecture discussion|suit yourself)\b/i, emotion: 'tease', intensity: 0.65 },
    { pattern: /\b(idiot|what are you saying|don't say that|stop it|embarrassing|not like that)\b/i, emotion: 'shy', intensity: 0.60 },
    { pattern: /\b(thank you|that's sweet|i appreciate|i'm glad|always here|by your side|happy to help|anytime|aswani-kun)\b/i, emotion: 'touched', intensity: 0.52 },
    { pattern: /\b(really\?|are you sure|doubt it|that doesn't sound right|that doesn't make sense|i'm not convinced)\b/i, emotion: 'skeptical', intensity: 0.55 },
    { pattern: /\b(we did it|you got it|that's amazing|excellent|congratulations|nice work|fantastic)\b/i, emotion: 'excited', intensity: 0.62 },
    { pattern: /\b(oh\b|wow|whoa|no way|really\b|seriously)\b/i, emotion: 'surprised', intensity: 0.48 },
    { pattern: /\b(i'm sorry|that sounds hard|that sounds painful|take your time|i'm here with you)\b/i, emotion: 'sad', intensity: 0.38 },
    { pattern: /\b(glad for you|happy for you|proud of you|wonderful|love that|good news)\b/i, emotion: 'happy', intensity: 0.48 },
    { pattern: /\b(hmm|well\b|let me think|let's see|probably|maybe|i'd say|the reason is|because)\b|\?/i, emotion: 'think', intensity: 0.38 },
];

const USER_MOOD_TO_RESPONSE = {
    sad: 'sad',
    stressed: 'calm',
    frustrated: 'calm',
    excited: 'happy',
    happy: 'happy',
    calm: 'calm',
    neutral: 'calm',
};

let responseInProgress = false;
let activeAssistantEmotion = '';

function updateAssistantExpression(text) {
    if (!text) return;
    if (!responseInProgress) {
        responseInProgress = true;
        activeAssistantEmotion = 'calm';
        character.setEmotion('calm', 0.25);
    }

    for (const cue of ASSISTANT_EXPRESSION_CUES) {
        if (cue.pattern.test(text) && cue.emotion !== activeAssistantEmotion) {
            activeAssistantEmotion = cue.emotion;
            character.setEmotion(cue.emotion, cue.intensity);
            break;
        }
    }
}

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
        updateAssistantExpression(params.text);
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
        character.prepareVisemes(ttsTextBuffer);
    }
    if (params.done) {
        chat.finalizeMessage();
        streamingActive = false;
        responseInProgress = false;
        activeAssistantEmotion = '';
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
    // Always queue: even done-only chunks (empty audio, done=true) must be
    // enqueued so the drain loop knows when synthesis has ended.
    if (!params.audio && !params.done) return; // skip truly empty chunks
    // Ensure analyser is connected before first chunk
    if (audio.analyser && !character.analyser) {
        character.setAnalyser(audio.analyser);
    }
    audio.queueChunk(params.audio || '', params.sample_rate, !!params.done);
    // Feed the running audio duration so the viseme schedule stays scaled to reality
    if (params.audio) {
        character.setVisemeDuration(audio.getKnownDuration());
    }
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

ws.on('config.changed', (params) => {
    console.log('[Config]', params.key, '=', params.value);
});

// Dance request from the backend — fires for both voice and chat turns
// (intent detection happens in the orchestrator's HandleInput).
ws.on('companion.dance', () => {
    character.dance();
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
            const b64 = (() => { const u8 = new Uint8Array(buf); let s = ''; for (let i = 0; i < u8.length; i++) s += String.fromCharCode(u8[i]); return btoa(s); })();
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
