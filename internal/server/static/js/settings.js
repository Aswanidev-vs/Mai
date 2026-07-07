// Settings panel logic

class SettingsPanel {
    constructor(wsClient) {
        this.ws = wsClient;
        this.panel = document.getElementById('settingsPanel');
        this.toggleBtn = document.getElementById('settingsToggle');
        this.closeBtn = document.getElementById('settingsClose');

        this._setupUI();
    }

    _setupUI() {
        this.toggleBtn.addEventListener('click', () => this.toggle());
        this.closeBtn.addEventListener('click', () => this.close());

        // Setting change handlers
        document.getElementById('settingLLMProvider').addEventListener('change', (e) => {
            this._update('llm.provider', e.target.value);
        });

        document.getElementById('settingLLMModel').addEventListener('change', (e) => {
            this._update('llm.model', e.target.value);
        });

        const speedSlider = document.getElementById('settingTTSSpeed');
        const speedValue = document.getElementById('ttsSpeedValue');
        speedSlider.addEventListener('input', (e) => {
            speedValue.textContent = e.target.value;
        });
        speedSlider.addEventListener('change', (e) => {
            this._update('tts.speed', parseFloat(e.target.value));
        });

        document.getElementById('settingVoiceStyle').addEventListener('change', (e) => {
            this._update('tts.voice_style', e.target.value);
        });

        document.getElementById('settingHybridMode').addEventListener('change', (e) => {
            this._update('llm.hybrid_mode', e.target.checked);
        });
    }

    toggle() {
        this.panel.classList.toggle('open');
    }

    close() {
        this.panel.classList.remove('open');
    }

    _update(key, value) {
        if (this.ws && this.ws.connected) {
            this.ws.send('config.update', { key, value });
        }
    }

    applyConfig(config) {
        if (config.llm) {
            if (config.llm.provider) {
                document.getElementById('settingLLMProvider').value = config.llm.provider;
            }
            if (config.llm.model) {
                document.getElementById('settingLLMModel').value = config.llm.model;
            }
        }
    }
}
