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

ws.on('chat.response', (params) => {
    if (params.text) {
        if (!streamingActive) {
            chat.startAgentMessage();
            streamingActive = true;
        }
        chat.streamToken(params.text);
    }
    if (params.done) {
        chat.finalizeMessage();
        streamingActive = false;
    }
});

// Status changes
ws.on('status.changed', (params) => {
    const status = params.status || 'idle';
    statusText.textContent = status.charAt(0).toUpperCase() + status.slice(1);

    statusIndicator.className = 'status-indicator connected';
    if (status === 'thinking') {
        statusIndicator.classList.add('thinking');
    } else if (status === 'speaking') {
        statusIndicator.classList.add('speaking');
        character.setSpeaking(true);
    } else {
        character.setSpeaking(false);
    }
});

// TTS audio chunks
ws.on('tts.chunk', (params) => {
    if (params.audio) {
        audio.playChunk(params.audio, params.sample_rate);
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

ws.connect();
document.getElementById('chatInput').focus();
