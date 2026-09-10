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
    // tease
    { pattern: /\b(heh|as expected|aren't you|you really are|don't flatter yourself|making things complicated|architecture discussion|suit yourself|you're such a|got me there|well played|as expected of you)\b/i, emotion: 'tease', intensity: 0.65 },
    // shy
    { pattern: /\b(idiot|what are you saying|don't say that|stop it|embarrassing|not like that|shut up|you're embarrassing me|i'm blushing|so forward)\b/i, emotion: 'shy', intensity: 0.60 },
    // touched
    { pattern: /\b(thank you|that's sweet|i appreciate|i'm glad|always here|by your side|happy to help|anytime|aswani-kun|you mean a lot|that means a lot|i care about you)\b/i, emotion: 'touched', intensity: 0.52 },
    // skeptical
    { pattern: /\b(really\?|are you sure|doubt it|that doesn't sound right|that doesn't make sense|i'm not convinced|seriously\?|you can't be serious|i'll believe it when i see it)\b/i, emotion: 'skeptical', intensity: 0.55 },
    // excited
    { pattern: /\b(we did it|you got it|that's amazing|excellent|congratulations|nice work|fantastic|that's incredible|so cool|love it|finally|im so excited|perfect|i can't wait|can't wait|so fun|how fun|that's so fun|yay|woohoo|awesome|brilliant|i'm thrilled|so pumped)\b/i, emotion: 'excited', intensity: 0.62 },
    // surprised
    { pattern: /\b(oh\b|wow|whoa|no way|really\b|seriously|wait what|hold on|huh\?|wait, really|that's surprising|i didn't expect that|unexpected|i'm shocked|what a surprise|oh my|that's unexpected)\b/i, emotion: 'surprised', intensity: 0.48 },
    // sad
    { pattern: /\b(i'm sorry|that sounds hard|that sounds painful|take your time|i'm here with you|that's rough|i feel for you|that's so sad|oh no|that's unfortunate|i'm sorry to hear|that's disappointing|sad to see|i wish i could help more|my heart goes out|i'm worried)\b/i, emotion: 'sad', intensity: 0.38 },
    // happy
    { pattern: /\b(glad for you|happy for you|proud of you|wonderful|love that|good news|great to hear|i'm happy to|that's great|i'm so happy|that makes me happy|delighted|thrilled|so glad|how wonderful|lovely|that's wonderful|i love that|makes me smile)\b/i, emotion: 'happy', intensity: 0.48 },
    // think (hesitation / reasoning)
    { pattern: /\b(hmm|well\b|let me think|let's see|uhm|uhh|huh|let me check|give me a second|one moment|probably|maybe|i'd say|the reason is|because|let me think about that|i'm thinking|interesting question|good question|let me consider|hmm, let's see|i need a moment)\b|\?/i, emotion: 'think', intensity: 0.38 },
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
let activeAssistantIntensity = 0;
let assistantDecayTimer = null;

function updateAssistantExpression(text) {
    if (!text) return;
    if (!responseInProgress) {
        responseInProgress = true;
        activeAssistantEmotion = 'calm';
        activeAssistantIntensity = 0.25;
        character.setEmotion('calm', 0.25);
    }

    // Score every cue instead of stopping at the first match: the emotional
    // tone should follow the *strongest, most recent* cue, not whichever regex
    // happens to be listed first. Recency is a small tiebreaker so a late
    // "we did it!" outweighs an early "let me think".
    let best = null;
    let bestScore = -1;
    for (const cue of ASSISTANT_EXPRESSION_CUES) {
        cue.pattern.lastIndex = 0;
        const m = cue.pattern.exec(text);
        if (!m) continue;
        const recency = m.index / Math.max(text.length, 1);
        const score = cue.intensity + recency * 0.25;
        if (score > bestScore) {
            bestScore = score;
            best = cue;
        }
    }
    if (!best) return;

    // Re-anchor when the emotion changes, or when a clearly stronger cue for
    // the same emotion arrives — avoids flicker between subtle cues.
    const changed = best.emotion !== activeAssistantEmotion;
    const stronger = best.intensity > activeAssistantIntensity + 0.1;
    if (changed || stronger) {
        activeAssistantEmotion = best.emotion;
        activeAssistantIntensity = best.intensity;
        character.setEmotion(best.emotion, best.intensity);
    }
}

// Gentle decay: after Mai finishes speaking her expression eases back toward
// neutral over ~1.5s instead of snapping. Each step lowers the intensity and
// re-applies the same emotion; character.setEmotion re-arms its own reset
// timer each call, so the fade stays smooth until we settle on calm/neutral.
function decayAssistantExpression() {
    if (assistantDecayTimer) clearTimeout(assistantDecayTimer);
    const step = () => {
        if (responseInProgress) return; // still talking — don't fade mid-sentence
        if (!activeAssistantEmotion || activeAssistantIntensity <= 0.05) {
            activeAssistantEmotion = '';
            activeAssistantIntensity = 0;
            character.setEmotion('calm', 0.25);
            return;
        }
        activeAssistantIntensity = Math.max(0, activeAssistantIntensity - 0.09);
        character.setEmotion(activeAssistantEmotion, activeAssistantIntensity);
        assistantDecayTimer = setTimeout(step, 300);
    };
    assistantDecayTimer = setTimeout(step, 450);
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
        // Let the expression gently decay back toward neutral rather than snapping.
        decayAssistantExpression();
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

// Mai's own sentiment (published by the orchestrator from her response text).
// This drives her expression from what SHE is feeling — independent of and
// complementary to the user's mood. Backend labels (happy, sad, excited,
// frustrated, stressed, calm, neutral) all map 1:1 to EMOTION_MAP keys.
ws.on('emotion.mai', (params) => {
    const emotion = params.emotion || 'neutral';
    const intensity = Math.min(0.7, Math.max(0.2, Number(params.intensity) || 0.4));
    // Track it so the same gentle decay applies once she finishes speaking.
    if (assistantDecayTimer) clearTimeout(assistantDecayTimer);
    activeAssistantEmotion = emotion;
    activeAssistantIntensity = intensity;
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
