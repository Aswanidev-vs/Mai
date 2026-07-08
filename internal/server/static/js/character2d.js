// 2D Sprite Puppet Character — lightweight alternative to VRM 3D rendering.
// Layers the PSD cutout images and animates them with CSS transforms + JS timing.

const LAYER_ORDER = [
    'back_hair', 'legwear', 'footwear', 'topwear', 'handwear',
    'neck', 'face', 'ears', 'eyewhite', 'irides', 'eyelash',
    'eyebrow', 'nose', 'mouth', 'front_hair', 'headwear'
];

// Layer positions from PSD (768×768 canvas, character centered at ~384,379)
const LAYER_POS = {
    back_hair:  { x: -95, y: -361, z: 0 },
    legwear:    { x: -78, y: -94,  z: 1 },
    footwear:   { x: -69, y: 254,  z: 2 },
    topwear:    { x: -94, y: -248, z: 3 },
    handwear:   { x: -101, y: -232, z: 4 },
    neck:       { x: -6,  y: -275, z: 5 },
    face:       { x: -39, y: -342, z: 6 },
    ears:       { x: -33, y: -302, z: 7 },
    eyewhite:   { x: -25, y: -297, z: 8 },
    irides:     { x: -22, y: -297, z: 9 },
    eyelash:    { x: -29, y: -300, z: 10 },
    eyebrow:    { x: -29, y: -306, z: 11 },
    nose:       { x: -8,  y: -282, z: 12 },
    mouth:      { x: -2,  y: -267, z: 13 },
    front_hair: { x: -53, y: -355, z: 14 },
    headwear:   { x: -13, y: -327, z: 15 },
};

// Animatable parts (indices into LAYER_ORDER for quick lookup)
const PARTS = {
    eyelash:  LAYER_ORDER.indexOf('eyelash'),
    irides:   LAYER_ORDER.indexOf('irides'),
    mouth:    LAYER_ORDER.indexOf('mouth'),
    eyebrow:  LAYER_ORDER.indexOf('eyebrow'),
    face:     LAYER_ORDER.indexOf('face'),
};

class Character2D {
    constructor(containerId) {
        this.container = document.getElementById(containerId);
        this.loaded = false;
        this.speaking = false;
        this.currentEmotion = 'neutral';
        this.restMode = false;
        this.lastInteraction = performance.now();
        this.userTyping = false;

        // Animation state
        this.blinkTimer = 0;
        this.nextBlink = 2 + Math.random() * 4;
        this.isBlinking = false;
        this.blinkProgress = 0;

        this.swayPhase = 0;
        this.breathPhase = 0;

        // Viseme schedule
        this.visemeSchedule = null;
        this.visemeDuration = 0;
        this.audioPlayer = null;

        // Emotion
        this.emotionTimer = 0;
        this.emotionTarget = null;

        // Idle behavior
        this.idleTimer = 0;
        this.idleBehavior = null;
        this.idleBehaviorT = 0;

        // Head spring
        this.headYaw = 0;
        this.headPitch = 0;

        // Mouse tracking
        this.mouseX = 0;
        this.mouseY = 0;
        this.mouseActive = false;
        this.mouseIdleTimer = 0;
        this._onMouseMove = (e) => {
            this.mouseX = (e.clientX / window.innerWidth) * 2 - 1;
            this.mouseY = -(e.clientY / window.innerHeight) * 2 + 1;
            this.mouseActive = true;
            this.mouseIdleTimer = 0;
        };
        window.addEventListener('mousemove', this._onMouseMove);

        this._loadLayers();
    }

    _loadLayers() {
        const imgPath = '/assets/mai2d/';
        let loaded = 0;
        this.layerEls = [];

        // Create the puppet container
        this.puppet = document.createElement('div');
        this.puppet.style.cssText = 'position:relative;width:100%;height:100%;display:flex;align-items:center;justify-content:center;overflow:hidden;';
        this.container.appendChild(this.puppet);

        for (const name of LAYER_ORDER) {
            const img = new Image();
            img.src = `${imgPath}${name}.png`;
            img.alt = name;
            img.style.cssText = `position:absolute;pointer-events:none;image-rendering:auto;`;
            const pos = LAYER_POS[name];
            img.style.left = `calc(50% + ${pos.x}px)`;
            img.style.top = `calc(50% + ${pos.y}px)`;
            img.style.zIndex = pos.z;
            img.style.transformOrigin = 'center bottom';

            img.onload = () => {
                loaded++;
                if (loaded === LAYER_ORDER.length) {
                    this.loaded = true;
                    console.log('[2D] All layers loaded');
                    this._animate();
                }
            };
            img.onerror = () => {
                loaded++;
                console.warn(`[2D] Failed to load ${name}`);
                if (loaded === LAYER_ORDER.length) {
                    this.loaded = true;
                    this._animate();
                }
            };

            this.puppet.appendChild(img);
            this.layerEls.push(img);
        }
    }

    // ── Public API (matches CharacterRenderer) ──
    setAnalyser(a) { this.analyser = a; }
    setAudioPlayer(player) { this.audioPlayer = player; }

    prepareVisemes(text) {
        const sched = buildVisemeSchedule(text);
        this.visemeSchedule = sched.segments;
    }

    setVisemeDuration(d) {
        if (d > 0) this.visemeDuration = d;
    }

    setSpeaking(s) {
        this.speaking = s;
        if (s) this.lastInteraction = performance.now();
        if (!s) {
            this.visemeSchedule = null;
            this.visemeDuration = 0;
        }
    }

    setEmotion(emotion, intensity) {
        this.currentEmotion = emotion || 'neutral';
        this.emotionTarget = emotion;
        this.emotionTimer = 4000;
        this.lastInteraction = performance.now();
    }

    setStatus(status) {
        this.agentStatus = status;
        if (status !== 'idle') this.lastInteraction = performance.now();
    }

    setUserTyping(b) {
        this.userTyping = !!b;
        if (b) this.lastInteraction = performance.now();
    }

    notifyUserPresent() {
        this.lastInteraction = performance.now();
    }

    // ── Animation Loop ──
    _animate() {
        if (!this.loaded) return;
        requestAnimationFrame(() => this._animate());

        const now = performance.now();
        const dt = Math.min((now - (this._lastFrame || now)) / 1000, 0.1);
        this._lastFrame = now;

        this._updateBlink(dt);
        this._updateMouth(dt);
        this._updateHead(dt);
        this._updateIdleBehavior(dt);
        this._updateRestMode();
    }

    _updateBlink(dt) {
        this.blinkTimer += dt;
        const eyelashEl = this.layerEls[PARTS.eyelash];
        if (!eyelashEl) return;

        if (!this.isBlinking && this.blinkTimer >= this.nextBlink) {
            this.isBlinking = true;
            this.blinkProgress = 0;
        }

        if (this.isBlinking) {
            this.blinkProgress += dt / 0.15;
            if (this.blinkProgress >= 1) {
                this.isBlinking = false;
                this.blinkTimer = 0;
                this.nextBlink = this.restMode
                    ? 3 + Math.random() * 8
                    : 1.5 + Math.random() * 4;
                eyelashEl.style.opacity = '1';
            } else {
                // Blink: squeeze eyelash vertically
                const v = Math.sin(Math.PI * this.blinkProgress);
                eyelashEl.style.transform = `scaleY(${1 - v * 0.9})`;
            }
        } else {
            eyelashEl.style.transform = 'scaleY(1)';
        }
    }

    _updateMouth(dt) {
        const mouthEl = this.layerEls[PARTS.mouth];
        if (!mouthEl) return;

        let openAmount = 0;

        if (this.speaking && this.visemeSchedule && this.visemeSchedule.length > 0 && this.visemeDuration > 0 && this.audioPlayer) {
            const playhead = this.audioPlayer.getPlayhead();
            const phase = Math.max(0, Math.min(1, playhead / this.visemeDuration));
            const seg = visemeSegmentAt(this.visemeSchedule, phase);
            const rms = this.analyser ? this._computeRMS() : 0;
            const gate = energyGate(rms, 0.012, 0.10);
            openAmount = seg ? seg.open * (0.1 + 0.9 * gate) : 0;
        } else if (this.emotionTarget) {
            // Emotion mouth hints
            const mouthEmotions = { happy: 0.15, sad: 0.1, angry: 0.12, surprised: 0.25 };
            openAmount = mouthEmotions[this.emotionTarget] || 0;
        }

        // Smooth the mouth opening
        this._mouthCurrent = this._mouthCurrent || 0;
        const rate = openAmount > this._mouthCurrent ? 55 : 28;
        this._mouthCurrent += (openAmount - this._mouthCurrent) * (1 - Math.exp(-rate * dt));

        // Apply: scaleY for jaw drop, small translate for natural movement
        const jawDrop = this._mouthCurrent * 0.8;
        const lipLift = this._mouthCurrent * 0.15;
        mouthEl.style.transform = `scaleY(${1 + jawDrop}) translateY(${-lipLift}px)`;
        mouthEl.style.opacity = String(Math.min(1, 0.6 + this._mouthCurrent * 0.4));
    }

    _updateHead(dt) {
        // Subtle head sway + mouse tracking
        this.swayPhase += dt * 0.3;
        this.breathPhase += dt * 0.8;

        const sway = Math.sin(this.swayPhase) * 1.5;
        const breathe = Math.sin(this.breathPhase) * 0.5;

        // Mouse tracking: shift head slightly toward cursor
        let mouseOffsetX = 0;
        let mouseOffsetY = 0;
        if (this.mouseActive) {
            this.mouseIdleTimer += dt;
            if (this.mouseIdleTimer < 2) {
                mouseOffsetX = this.mouseX * 3;
                mouseOffsetY = this.mouseY * 2;
            } else {
                this.mouseActive = false;
            }
        }

        // Typing reaction: tilt toward chat panel
        if (this.userTyping && !this.mouseActive) {
            mouseOffsetX = Math.max(mouseOffsetX, -2);
        }

        const totalYaw = sway + mouseOffsetX;
        const totalPitch = breathe + mouseOffsetY;

        // Apply to face group (face + eyes + mouth + nose + eyebrows)
        for (const idx of [PARTS.face, PARTS.eyewhite, PARTS.irides, PARTS.eyebrow, PARTS.nose, PARTS.mouth]) {
            const el = this.layerEls[idx];
            if (el) {
                el.style.transform = `translate(${totalYaw * 0.3}px, ${totalPitch * 0.2}px)`;
            }
        }
        // Hair moves less (it's further back)
        for (const name of ['front_hair', 'back_hair', 'headwear']) {
            const idx = LAYER_ORDER.indexOf(name);
            const el = this.layerEls[idx];
            if (el) {
                el.style.transform = `translate(${totalYaw * 0.15}px, ${totalPitch * 0.1}px)`;
            }
        }
        // Body sway (upper body)
        for (const name of ['topwear', 'handwear', 'neck']) {
            const idx = LAYER_ORDER.indexOf(name);
            const el = this.layerEls[idx];
            if (el) {
                el.style.transform = `translate(${sway * 0.1}px, ${breathe * 0.3}px)`;
            }
        }
    }

    _updateIdleBehavior(dt) {
        this.idleTimer += dt;
        if (!this.idleBehavior && this.idleTimer > 18 + Math.random() * 20) {
            this.idleTimer = 0;
            this.idleBehavior = Math.random() < 0.5 ? 'glance' : 'nod';
            this.idleBehaviorT = 0;
        }
        if (this.idleBehavior) {
            this.idleBehaviorT += dt;
            const p = Math.min(1, this.idleBehaviorT / 1.5);
            const env = Math.sin(p * Math.PI);
            if (this.idleBehavior === 'glance') {
                const idx = LAYER_ORDER.indexOf('front_hair');
                const el = this.layerEls[idx];
                if (el) el.style.transform = `translate(${env * 4}px, 0)`;
            } else {
                // Nod: brief head pitch
                for (const idx of [PARTS.face, PARTS.eyewhite, PARTS.irides]) {
                    const el = this.layerEls[idx];
                    if (el) el.style.transform = `translate(0, ${env * 3}px)`;
                }
            }
            if (p >= 1) this.idleBehavior = null;
        }
    }

    _updateRestMode() {
        const since = (performance.now() - this.lastInteraction) / 1000;
        this.restMode = since > 28;
    }

    _computeRMS() {
        if (!this.analyser) return 0;
        const data = new Uint8Array(this.analyser.fftSize);
        this.analyser.getByteTimeDomainData(data);
        let sum = 0;
        for (let i = 0; i < data.length; i++) {
            const v = (data[i] - 128) / 128;
            sum += v * v;
        }
        return Math.sqrt(sum / data.length);
    }
}

window.CharacterRenderer = Character2D;
