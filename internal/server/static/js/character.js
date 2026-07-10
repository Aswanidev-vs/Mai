import * as THREE from 'three';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';
import { VRMLoaderPlugin, VRMUtils } from '@pixiv/three-vrm';
import { VRMAnimationLoaderPlugin, createVRMAnimationClip } from '@pixiv/three-vrm-animation';

const CAMERA_FOV = 30;
const BLINK_CLOSE_DURATION = 0.08;  // 80ms closing (slightly slower for natural feel)
const BLINK_OPEN_MIN = 0.12;         // 120ms minimum opening (faster opening)
const BLINK_OPEN_MAX = 0.25;         // 250ms maximum opening
const BLINK_DELAY_MIN = 2.5;         // 2.5s minimum between blinks (more frequent)
const BLINK_DELAY_MAX = 6.0;         // 6s maximum between blinks
const DOUBLE_BLINK_CHANCE = 0.18;    // Slightly less frequent double blinks
const BLINK_SKIP_THRESHOLD = 0.15;
const PARTIAL_BLINK_CHANCE = 0.25;    // 25% chance of partial blink (more natural)
// Advanced lip sync constants (from Airi)
const LIP_ATTACK = 50;
const LIP_RELEASE = 30;
const LIP_CAP = 0.7;
const LIP_SILENCE_VOL = 0.04;
const LIP_SILENCE_GAIN = 0.05;
const LIP_IDLE_MS = 160;
const LIP_RELEASE_DURATION_MS = 200; // Smooth crossfade when speech ends (Airi: RELEASE_DURATION_MS)
const EXPRESSION_RESET_MS = 4000;

// Spring-damper head physics (Airi: stiffness=120, damping=16, mass=1)
const HEAD_SPRING_STIFFNESS = 120;
const HEAD_SPRING_DAMPING = 16;
const HEAD_SPRING_MASS = 1;
const HEAD_SNAP_THRESHOLD = 0.01;   // Snap to target when close enough (Airi)

// CPT-distributed eye saccade intervals (Airi: EYE_SACCADE_INT_STEP=400, EYE_SACCADE_INT_P)
const SACCADE_STEP = 400;
const SACCADE_CPT = [
    [0.075, 800],   // 7.5% chance → 800ms
    [0.110, 0],     // 11% → 1200ms
    [0.125, 0],     // 12.5% → 1600ms
    [0.140, 0],     // 14% → 2000ms
    [0.125, 0],     // 12.5% → 2400ms
    [0.050, 0],     // 5% → 2800ms
    [0.040, 0],     // 4% → 3200ms
    [0.030, 0],     // 3% → 3600ms
    [0.020, 0],     // 2% → 4000ms
    [1.000, 0],     // rest → 4400ms
];
// Build cumulative probability table
for (let i = 1; i < SACCADE_CPT.length; i++) {
    SACCADE_CPT[i][0] += SACCADE_CPT[i - 1][0];
    SACCADE_CPT[i][1] = SACCADE_CPT[i - 1][1] + SACCADE_STEP;
}

// "Alive" tuning (Airi-inspired)
const REST_IDLE_SECONDS = 25;       // time before she slips into a calmer "rest" state
const WANDER_CHANCE = 0.30;         // chance a gaze shift is a longer "look around the room"
const IDLE_BEHAVIOR_MIN = 10;       // seconds between occasional idle behaviors
const IDLE_BEHAVIOR_MAX = 25;
const BREATH_BASE_RATE = 0.75;      // Base breathing rate (slower = more relaxed)
const BREATH_VARIANCE = 0.15;       // Random variance in breathing
const SPONTANEOUS_SMILE_CHANCE = 0.15; // 15% chance to smile during idle check

const EMOTION_MAP = {
    happy:     { expression: [{ name: 'happy', value: 0.7 }], blendDuration: 0.4, facialKey: 'happy' },
    sad:       { expression: [{ name: 'sad', value: 0.65 }], blendDuration: 0.5, facialKey: 'sad' },
    angry:     { expression: [{ name: 'angry', value: 0.65 }], blendDuration: 0.3, facialKey: 'angry' },
    surprised: { expression: [{ name: 'surprised', value: 0.75 }], blendDuration: 0.15, facialKey: 'surprised' },
    neutral:   { expression: [{ name: 'neutral', value: 1.0 }], blendDuration: 0.6, facialKey: null },
    think:     { expression: [{ name: 'think', value: 0.6 }], blendDuration: 0.5, facialKey: null },
    calm:      { expression: [{ name: 'neutral', value: 1.0 }], blendDuration: 0.7, facialKey: null },
    stressed:  { expression: [{ name: 'angry', value: 0.5 }], blendDuration: 0.35, facialKey: 'angry' },
    excited:   { expression: [{ name: 'surprised', value: 0.6 }, { name: 'happy', value: 0.3 }], blendDuration: 0.2, facialKey: 'happy' },
    frustrated:{ expression: [{ name: 'angry', value: 0.55 }], blendDuration: 0.35, facialKey: 'angry' },
};

const VOWEL_MAP = { A: 'aa', E: 'ee', I: 'ih', O: 'oh', U: 'ou' };

const clamp = (val, min, max) => Math.min(Math.max(val, min), max);
const lerp = (a, b, t) => a + (b - a) * t;

// Crypto-secure random for Codacy compliance. Falls back to Math.random if crypto unavailable.
const secureRand = () => {
    const a = new Uint32Array(1);
    try { crypto.getRandomValues(a); return a[0] / 0x100000000; }
    catch (e) { return 0.5; }
};

class CharacterRenderer {
    constructor(containerId) {
        this.container = document.getElementById(containerId);
        this.vrm = null;
        this.vrmGroup = null;
        this.scene = null;
        this.camera = null;
        this.renderer = null;
        this.clock = new THREE.Clock();
        this.loaded = false;
        this.mixer = null;

        // Base positions and rotations for drift-free offsets
        this.hipsBaseX = null;
        this.hipsBaseY = null;
        this.hipsBaseZ = null;

        // Lip sync
        this.smoothedVowels = { aa: 0, ee: 0, ih: 0, oh: 0, ou: 0 };
        this.analyser = null;
        this.audioPlayer = null;
        this.timeData = null;
        this.speaking = false;

        // Viseme schedule (word-accurate lip sync)
        this.visemeSchedule = null;
        this.visemeDuration = 0;

        // Blink state (initialized lazily in _updateBlink)
        this._blinkState = null;

        // Gaze / "alive" gaze controller
        this.fixationTarget = new THREE.Vector3(0, 1.3, 0);
        this.defaultLookAt = new THREE.Vector3(0, 1.3, 0);
        this.eyeHeight = 1.3;
        this.gazePoint = new THREE.Vector3(0, 1.3, 0);   // current gaze destination
        this.nextGazeShift = this._cptSaccadeInterval();   // CPT-distributed intervals
        this.gazeTimer = 0;
        this.gazeWander = 0;                               // 0..1 how far she's looking away

        // Spring-damper head physics (Airi-style)
        this.headSpring = {
            targetPitch: 0, targetYaw: 0, targetRoll: 0,
            velPitch: 0, velYaw: 0, velRoll: 0,
            posPitch: 0, posYaw: 0, posRoll: 0,
        };

        // Lip sync release state (smooth crossfade when speech ends)
        this.lipSyncRelease = { remainingMs: 0, lastForcedValue: 0 };

        // Mouse tracking for eye/head follow
        this._onMouseMove = (e) => {
            this.mouseX = (e.clientX / window.innerWidth) * 2 - 1;
            this.mouseY = -(e.clientY / window.innerHeight) * 2 + 1;
            this.userPresent = true;
            this.lastInteraction = performance.now();
        };
        window.addEventListener('mousemove', this._onMouseMove);

        // Expression
        this.currentExpressionValues = new Map();
        this.targetExpressionValues = new Map();
        this.isTransitioning = false;
        this.transitionProgress = 0;
        this.currentEmotion = null;
        this.expressionResetTimer = null;
        this.agentStatus = 'idle';   // idle | thinking | speaking | listening

        // Enhanced micro-expressions (Airi-style)
        this.microExpressionTimer = 0;
        this.nextMicroExpression = 2 + secureRand() * 4; // More frequent (2-6s)
        this.microExpressionTargets = {};
        this.microExpressionCurrent = {};
        this.microExpressionActive = false;
        this.microExpressionDuration = 0;
        this.microExpressionProgress = 0;

        // Spontaneous smiling system
        this.spontaneousSmileTimer = 0;
        this.nextSmileTime = 8 + secureRand() * 12; // 8-20 seconds between potential smiles
        this.spontaneousSmileActive = false;
        this.spontaneousSmileDuration = 0;
        this.spontaneousSmileProgress = 0;
        this.spontaneousSmileIntensity = 0;

        // Posture / life state
        this.breathScale = 1;          // multiplied into breathing amplitude
        this.restMode = false;
        this.lastInteraction = performance.now();
        this.userTyping = false;
        this.userPresent = false;
        this.postureHead = { pitch: 0, yaw: 0, roll: 0 };  // smoothed emotion/life offsets
        this.idleBehaviorTimer = this._rand(IDLE_BEHAVIOR_MIN, IDLE_BEHAVIOR_MAX);
        this.idleBehavior = null;      // { type, t, dur }
        this.idleBehaviorT = 0;

        // Enhanced user responsiveness
        this.lastUserInteractionTime = performance.now();

        // Camera control system
        this.cameraDistance = 1.0; // Multiplier for base distance
        this.cameraOffsetX = 0;
        this.cameraOffsetY = 0;
        this.cameraOffsetZ = 0;
        this.baseCameraPosition = new THREE.Vector3();
        this.baseLookAt = new THREE.Vector3();

        this._init();
        this._loadModel();
        this._animate();
    }

    _rand(a, b) { return a + secureRand() * (b - a); }

    // CPT-distributed saccade interval (Airi: randomSaccadeInterval)
    // Probability-weighted: short intervals more likely than long ones
    _cptSaccadeInterval() {
        const r = secureRand();
        for (let i = 0; i < SACCADE_CPT.length; i++) {
            if (r <= SACCADE_CPT[i][0]) {
                return (SACCADE_CPT[i][1] + secureRand() * SACCADE_STEP) / 1000; // Convert ms→s
            }
        }
        return (SACCADE_CPT[SACCADE_CPT.length - 1][1] + secureRand() * SACCADE_STEP) / 1000;
    }

    // Spring-damper physics update (Airi: semi-implicit Euler)
    _springDamper(current, target, velocity, dt) {
        const accel = (HEAD_SPRING_STIFFNESS * (target - current) - HEAD_SPRING_DAMPING * velocity) / HEAD_SPRING_MASS;
        velocity += accel * dt;
        current += velocity * dt;
        // Snap to target when close enough
        if (Math.abs(target - current) < HEAD_SNAP_THRESHOLD && Math.abs(velocity) < HEAD_SNAP_THRESHOLD) {
            current = target;
            velocity = 0;
        }
        return { pos: current, vel: velocity };
    }

    // Smoothstep: 3t² - 2t³ (Airi lip sync release)
    _smoothstep(t) { return t * t * (3 - 2 * t); }

    async _init() {
        // ── Rendering Backend: WebGPU → WebGL fallback ──
        this.renderBackend = 'webgl'; // default
        this.renderer = null;

        // Try WebGPU first
        if (navigator.gpu) {
            try {
                const adapter = await navigator.gpu.requestAdapter();
                if (adapter) {
                    const device = await adapter.requestDevice();
                    if (device) {
                        this.renderer = new THREE.WebGPURenderer({ antialias: false, alpha: true });
                        this.renderBackend = 'webgpu';
                        console.log('[VRM] Using WebGPU renderer');
                    }
                }
            } catch (e) {
                console.warn('[VRM] WebGPU unavailable, falling back to WebGL:', e.message);
            }
        }

        // Fallback to WebGL
        if (!this.renderer) {
            this.renderer = new THREE.WebGLRenderer({
                antialias: false,
                alpha: true,
                powerPreference: 'high-performance',
            });
            this.renderBackend = 'webgl';
            console.log('[VRM] Using WebGL renderer');
        }

        this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 1.5));
        this.renderer.setClearColor(0x000000, 0); // Transparent — PixiJS handles background
        this.renderer.outputColorSpace = THREE.SRGBColorSpace;
        this.renderer.toneMapping = THREE.LinearToneMapping;
        this.renderer.toneMappingExposure = 1.15;
        this.renderer.shadowMap.enabled = false;
        this.container.appendChild(this.renderer.domElement);
        
        // Setup camera controls after renderer is created
        this._setupCameraControls();

        // Three.js scene + camera (transparent background)
        this.scene = new THREE.Scene();
        this.camera = new THREE.PerspectiveCamera(CAMERA_FOV, 1, 0.01, 100);

        // Warm lighting for VRM character
        this.scene.add(new THREE.AmbientLight(0xfff0e0, 0.9));
        const ptLight = new THREE.PointLight(0xffe8c8, 0.5, 20);
        ptLight.position.set(2, 2, -2);
        this.scene.add(ptLight);

        // ── PixiJS 2D Background Layer ──
        this._initPixiBackground();

        this._resize();
        window.addEventListener('resize', () => this._resize());
        if (window.ResizeObserver && this.container) {
            new ResizeObserver(() => this._resize()).observe(this.container);
        }
        requestAnimationFrame(() => this._resize());
    }

    // ── PixiJS Cozy Background ──
    _initPixiBackground() {
        if (typeof PIXI === 'undefined') {
            console.warn('[VRM] PixiJS not loaded, using CSS background fallback');
            this.container.style.background = 'linear-gradient(180deg, #f5e6d3 0%, #e8d5c0 40%, #d4c4a8 100%)';
            return;
        }

        this.pixiApp = new PIXI.Application({
            resizeTo: this.container,
            backgroundAlpha: 0,
            antialias: true,
            resolution: Math.min(window.devicePixelRatio, 2),
            autoDensity: true,
        });
        // Insert PixiJS canvas behind the Three.js canvas
        this.container.insertBefore(this.pixiApp.view, this.container.firstChild);
        this.pixiApp.view.style.position = 'absolute';
        this.pixiApp.view.style.top = '0';
        this.pixiApp.view.style.left = '0';
        this.pixiApp.view.style.zIndex = '0';

        const stage = this.pixiApp.stage;
        const w = this.container.clientWidth;
        const h = this.container.clientHeight;

        // ── Warm gradient background ──
        const bg = new PIXI.Graphics();
        bg.beginFill(0xf5e6d3);
        bg.drawRect(0, 0, w, h);
        bg.endFill();
        // Gradient overlay via a tall sprite
        const gradCanvas = document.createElement('canvas');
        gradCanvas.width = 1;
        gradCanvas.height = 256;
        const gCtx = gradCanvas.getContext('2d');
        const grad = gCtx.createLinearGradient(0, 0, 0, 256);
        grad.addColorStop(0, 'rgba(245,230,211,0)');
        grad.addColorStop(0.4, 'rgba(232,213,192,0.3)');
        grad.addColorStop(1, 'rgba(212,196,168,0.6)');
        gCtx.fillStyle = grad;
        gCtx.fillRect(0, 0, 1, 256);
        const gradTex = PIXI.Texture.from(gradCanvas);
        const gradSprite = new PIXI.Sprite(gradTex);
        gradSprite.width = w;
        gradSprite.height = h;
        bg.addChild(gradSprite);
        stage.addChild(bg);

        // ── Window ──
        const winX = w * 0.72;
        const winY = h * 0.18;
        const winW = w * 0.18;
        const winH = h * 0.45;
        // Frame
        const frame = new PIXI.Graphics();
        frame.beginFill(0x8b7355);
        frame.drawRect(winX - 6, winY - 6, winW + 12, winH + 12);
        frame.endFill();
        stage.addChild(frame);
        // Pane gradient
        const winCanvas = document.createElement('canvas');
        winCanvas.width = 128;
        winCanvas.height = 192;
        const wCtx = winCanvas.getContext('2d');
        const wGrad = wCtx.createLinearGradient(0, 0, 0, 192);
        wGrad.addColorStop(0, '#b8d4e8');
        wGrad.addColorStop(0.6, '#d4e8f0');
        wGrad.addColorStop(1, '#f0e8d8');
        wCtx.fillStyle = wGrad;
        wCtx.fillRect(0, 0, 128, 192);
        wCtx.strokeStyle = '#8b7355';
        wCtx.lineWidth = 3;
        wCtx.beginPath();
        wCtx.moveTo(64, 0); wCtx.lineTo(64, 192);
        wCtx.moveTo(0, 96); wCtx.lineTo(128, 96);
        wCtx.stroke();
        const winTex = PIXI.Texture.from(winCanvas);
        const winSprite = new PIXI.Sprite(winTex);
        winSprite.x = winX;
        winSprite.y = winY;
        winSprite.width = winW;
        winSprite.height = winH;
        stage.addChild(winSprite);
        // Warm glow
        const glowCanvas = document.createElement('canvas');
        glowCanvas.width = 128;
        glowCanvas.height = 128;
        const glCtx = glowCanvas.getContext('2d');
        const glGrad = glCtx.createRadialGradient(64, 64, 0, 64, 64, 64);
        glGrad.addColorStop(0, 'rgba(255,230,180,0.2)');
        glGrad.addColorStop(1, 'rgba(255,230,180,0)');
        glCtx.fillStyle = glGrad;
        glCtx.fillRect(0, 0, 128, 128);
        const glowTex = PIXI.Texture.from(glowCanvas);
        const glowSprite = new PIXI.Sprite(glowTex);
        glowSprite.x = winX - winW * 0.3;
        glowSprite.y = winY - winH * 0.2;
        glowSprite.width = winW * 1.6;
        glowSprite.height = winH * 1.4;
        stage.addChild(glowSprite);

        // ── Side table + plant ──
        const tableX = w * 0.1;
        const tableY = h * 0.7;
        const table = new PIXI.Graphics();
        table.beginFill(0x9c7a54);
        table.drawRect(tableX, tableY, w * 0.08, h * 0.12);
        table.endFill();
        table.beginFill(0x8b6940);
        table.drawRect(tableX - w * 0.01, tableY - h * 0.01, w * 0.1, h * 0.012);
        table.endFill();
        stage.addChild(table);
        // Pot
        const pot = new PIXI.Graphics();
        pot.beginFill(0xc4956a);
        pot.drawRect(tableX + w * 0.02, tableY - h * 0.04, w * 0.04, h * 0.035);
        pot.endFill();
        stage.addChild(pot);
        // Leaves
        const leaf = new PIXI.Graphics();
        leaf.beginFill(0x6b9b6b);
        for (let i = 0; i < 5; i++) {
            const angle = (i - 2) * 0.4;
            const lx = tableX + w * 0.04 + Math.sin(angle) * w * 0.025;
            const ly = tableY - h * 0.06 + Math.cos(angle) * -h * 0.04;
            leaf.drawEllipse(lx, ly, w * 0.008, h * 0.035);
        }
        leaf.endFill();
        stage.addChild(leaf);

        // ── Floor rug ──
        const rug = new PIXI.Graphics();
        rug.beginFill(0xc9a882, 0.5);
        rug.drawEllipse(w * 0.45, h * 0.92, w * 0.35, h * 0.06);
        rug.endFill();
        stage.addChild(rug);

        // ── Ambient particles (floating dust in window light) ──
        this.pixiParticles = [];
        for (let i = 0; i < 12; i++) {
            const p = new PIXI.Graphics();
            p.beginFill(0xffe8c8, 0.3 + Math.random() * 0.3);
            p.drawCircle(0, 0, 1 + Math.random() * 1.5);
            p.endFill();
            p.x = winX + Math.random() * winW;
            p.y = winY + Math.random() * winH;
            p._vx = (Math.random() - 0.5) * 0.15;
            p._vy = -0.05 - Math.random() * 0.1;
            p._life = Math.random() * 200;
            stage.addChild(p);
            this.pixiParticles.push(p);
        }

        // Animate particles
        this.pixiApp.ticker.add(() => {
            for (const p of this.pixiParticles) {
                p.x += p._vx;
                p.y += p._vy;
                p._life++;
                p.alpha = 0.3 * Math.sin((p._life % 200) / 200 * Math.PI);
                if (p.y < winY - 10 || p._life > 200) {
                    p.x = winX + Math.random() * winW;
                    p.y = winY + winH * 0.8 + Math.random() * winH * 0.2;
                    p._life = 0;
                }
            }
        });
    }

    _resize() {
        const w = this.container.clientWidth;
        const h = this.container.clientHeight;
        if (w === 0 || h === 0) return;
        this.renderer.setSize(w, h);
        this.camera.aspect = w / h;
        this.camera.updateProjectionMatrix();
        // PixiJS resizes itself via resizeTo, but ensure canvas is behind Three.js
        if (this.pixiApp?.view) {
            this.pixiApp.view.style.zIndex = '0';
            this.renderer.domElement.style.position = 'relative';
            this.renderer.domElement.style.zIndex = '1';
        }
    }

    // ── Camera Control System (X,Y,Z axis control) ──
    _setupCameraControls() {
        // Scroll wheel for Z-axis zoom
        this.renderer.domElement.addEventListener('wheel', (e) => {
            e.preventDefault();
            const zoomSpeed = 0.001;
            this.cameraDistance = Math.max(0.5, Math.min(3.0, this.cameraDistance - e.deltaY * zoomSpeed));
            this._updateCameraPosition();
        }, { passive: false });

        // Mouse drag for X,Y axis camera movement
        let isDragging = false;
        let lastMouseX = 0;
        let lastMouseY = 0;

        this.renderer.domElement.addEventListener('mousedown', (e) => {
            isDragging = true;
            lastMouseX = e.clientX;
            lastMouseY = e.clientY;
        });

        window.addEventListener('mouseup', () => {
            isDragging = false;
        });

        window.addEventListener('mousemove', (e) => {
            if (!isDragging) return;
            const deltaX = e.clientX - lastMouseX;
            const deltaY = e.clientY - lastMouseY;
            
            const moveSpeed = 0.002;
            this.cameraOffsetX -= deltaX * moveSpeed;
            this.cameraOffsetY += deltaY * moveSpeed;
            
            lastMouseX = e.clientX;
            lastMouseY = e.clientY;
            
            this._updateCameraPosition();
        });

        // Trackpad scroll for Z-axis (alternative to mouse wheel)
        this.renderer.domElement.addEventListener('touchstart', (e) => {
            if (e.touches.length === 2) {
                // Two-finger pinch for zoom
                this.lastTouchDistance = this._getTouchDistance(e.touches);
            }
        });

        this.renderer.domElement.addEventListener('touchmove', (e) => {
            if (e.touches.length === 2) {
                e.preventDefault();
                const currentDistance = this._getTouchDistance(e.touches);
                const delta = this.lastTouchDistance - currentDistance;
                const zoomSpeed = 0.005;
                this.cameraDistance = Math.max(0.5, Math.min(3.0, this.cameraDistance + delta * zoomSpeed));
                this.lastTouchDistance = currentDistance;
                this._updateCameraPosition();
            }
        }, { passive: false });
    }

    _getTouchDistance(touches) {
        const dx = touches[0].clientX - touches[1].clientX;
        const dy = touches[0].clientY - touches[1].clientY;
        return Math.sqrt(dx * dx + dy * dy);
    }

    _updateCameraPosition() {
        if (!this.baseCameraPosition.length()) return;
        
        // Apply distance multiplier to Z
        const newZ = this.baseCameraPosition.z * this.cameraDistance + this.cameraOffsetZ;
        
        // Apply X,Y offsets
        const newX = this.baseCameraPosition.x + this.cameraOffsetX;
        const newY = this.baseCameraPosition.y + this.cameraOffsetY;
        
        this.camera.position.set(newX, newY, newZ);
        this.camera.lookAt(this.baseLookAt.x + this.cameraOffsetX, this.baseLookAt.y + this.cameraOffsetY, this.baseLookAt.z);
    }

    // ── Cozy Background System ──
    _loadModel() {
        const loader = new GLTFLoader();
        loader.register((parser) => new VRMLoaderPlugin(parser));
        loader.register((parser) => new VRMAnimationLoaderPlugin(parser));

        // Load VRM + VRMA + motion3 files in parallel
        const motionPromises = MOTION_FILES.map(url =>
            loadMotion3(url).catch(e => { console.warn(`[VRM] Failed to load motion: ${url}`, e); return null; })
        );

        Promise.all([
            new Promise((resolve) => loader.load('/assets/mai.vrm', resolve)),
            new Promise((resolve) => loader.load('/assets/idle_loop.vrma', resolve)),
            Promise.all(motionPromises),
        ]).then(([vrmGltf, vrmaGltf, motionClips]) => {
            const vrm = vrmGltf.userData.vrm;
            if (!vrm) { console.error('[VRM] No VRM data'); return; }

            VRMUtils.removeUnnecessaryVertices(vrm.scene);
            VRMUtils.combineSkeletons(vrm.scene);
            vrm.scene.traverse((obj) => { 
                obj.frustumCulled = false; 
                // Aggressive optimization for performance
                if (obj.isMesh && obj.material) {
                    obj.material.flatShading = false; // Keep smooth shading for quality
                    if (obj.material.map) {
                        obj.material.map.anisotropy = 1; // Reduce texture filtering
                    }
                }
            });

            this.vrmGroup = new THREE.Group();
            this.vrmGroup.add(vrm.scene);
            // Removed rotation - let model face its default direction
            this.scene.add(this.vrmGroup);

            vrm.springBoneManager?.reset();
            // Optimize spring bones for performance
            if (vrm.springBoneManager) {
                vrm.springBoneManager.colliderGroups.forEach(group => {
                    group.colliders.forEach(collider => {
                        if (collider.radius) collider.radius *= 0.9; // Slightly reduce collider size
                    });
                });
            }
            this.vrmGroup.updateMatrixWorld(true);
            this.vrm = vrm;

            if (!this.vrm.lookAt.target) {
                this.vrm.lookAt.target = new THREE.Object3D();
            }
            this.scene.add(this.vrm.lookAt.target);

            // Store motion3 clips for playback
            this.motionClips = motionClips.filter(Boolean);
            this.currentMotion = null;
            this.motionTime = 0;
            this.motionPlaying = false;
            console.log(`[VRM] Loaded ${this.motionClips.length} motion clips`);

            // Auto-play first motion for natural idle movement
            if (this.motionClips.length > 0) {
                this.playMotion(0);
            }

            try {
                const vrmaAnims = vrmaGltf?.userData?.vrmAnimations;
                if (vrmaAnims && vrmaAnims.length > 0) {
                    const clip = createVRMAnimationClip(vrmaAnims[0], vrm);

                    // Strip arm/hand/shoulder/finger tracks — we control those ourselves.
                    const armBones = [
                        'leftShoulder', 'rightShoulder',
                        'leftUpperArm', 'rightUpperArm',
                        'leftLowerArm', 'rightLowerArm',
                        'leftHand', 'rightHand',
                        'leftThumbMetacarpal', 'leftThumbProximal', 'leftThumbDistal',
                        'leftIndexProximal', 'leftIndexIntermediate', 'leftIndexDistal',
                        'leftMiddleProximal', 'leftMiddleIntermediate', 'leftMiddleDistal',
                        'leftRingProximal', 'leftRingIntermediate', 'leftRingDistal',
                        'leftLittleProximal', 'leftLittleIntermediate', 'leftLittleDistal',
                        'rightThumbMetacarpal', 'rightThumbProximal', 'rightThumbDistal',
                        'rightIndexProximal', 'rightIndexIntermediate', 'rightIndexDistal',
                        'rightMiddleProximal', 'rightMiddleIntermediate', 'rightMiddleDistal',
                        'rightRingProximal', 'rightRingIntermediate', 'rightRingDistal',
                        'rightLittleProximal', 'rightLittleIntermediate', 'rightLittleDistal',
                    ];
                    clip.tracks = clip.tracks.filter(track => {
                        return !armBones.some(bone => track.name.includes(bone));
                    });

                    this.mixer = new THREE.AnimationMixer(vrm.scene);
                    const action = this.mixer.clipAction(clip);
                    action.play();
                }
            } catch (e) {
                console.error('[VRM] VRMA playback error:', e);
            }

            this._frameModel();
            this.loaded = true;
            console.log('[VRM] Ready');
        }).catch((err) => console.error('[VRM] Load error:', err));
    }

    // ── Motion3 Playback ──
    playMotion(index) {
        if (!this.motionClips || index >= this.motionClips.length) return;
        this.currentMotion = this.motionClips[index];
        this.motionTime = 0;
        this.motionPlaying = true;
        console.log(`[VRM] Playing motion: ${this.currentMotion.name}`);
    }

    stopMotion() {
        this.motionPlaying = false;
        this.currentMotion = null;
    }

    _updateMotion3(delta) {
        if (!this.motionPlaying || !this.currentMotion || !this.vrm?.expressionManager) return;

        this.motionTime += delta;

        // Apply parameter curves to VRM model
        for (const paramId of Object.keys(PARAM_MAP)) {
            const value = this.currentMotion.evaluate(paramId, this.motionTime);
            if (value !== null) {
                this.vrm.expressionManager.setValue(paramId, value);
            }
        }

        // Handle loop/stop
        if (!this.currentMotion.loop && this.motionTime >= this.currentMotion.duration) {
            this.stopMotion();
        }
    }

    _frameModel() {
        if (!this.vrm?.scene) return;

        const box = new THREE.Box3();
        const tmp = new THREE.Box3();
        this.vrm.scene.updateMatrixWorld(true);

        this.vrm.scene.traverse((obj) => {
            if (!obj.visible || !obj.isMesh || !obj.geometry) return;
            if (obj.name.startsWith('VRMC_springBone_collider')) return;
            if (!obj.geometry.boundingBox) obj.geometry.computeBoundingBox();
            tmp.copy(obj.geometry.boundingBox);
            tmp.applyMatrix4(obj.matrixWorld);
            box.union(tmp);
        });

        const size = new THREE.Vector3();
        const center = new THREE.Vector3();
        box.getSize(size);
        box.getCenter(center);

        const rad = (CAMERA_FOV / 2 * Math.PI) / 180;
        // Frame from head to waist — aim camera at upper body, not full-body center
        const lookY = center.y + size.y * 0.25; // Aim higher (face area)
        const zDist = (size.y * 0.25) / Math.tan(rad); // Much closer to match Airi's framing

        // Store base positions for camera control
        this.baseCameraPosition.set(center.x, lookY, center.z + zDist);
        this.baseLookAt.set(center.x, lookY, center.z);
        
        // Apply current camera controls
        this._updateCameraPosition();

        this.eyeHeight = lookY;
        // Fix "looking up": aim her gaze a touch BELOW eye height so she meets the lens straight-on.
        this.defaultLookAt.set(this.camera.position.x, this.eyeHeight - 0.015, this.camera.position.z);
        this.gazePoint.copy(this.defaultLookAt);
        this.fixationTarget.copy(this.defaultLookAt);
    }

    // ── Lip Sync Setup ──
    setAnalyser(a) {
        this.analyser = a;
        if (a) this.timeData = new Uint8Array(a.fftSize);
    }

    setAudioPlayer(player) {
        this.audioPlayer = player;
    }

    // ── Advanced Lip Sync (Airi-style winner+runner + smoothstep release) ──
    _updateLipSync(delta) {
        if (!this.vrm?.expressionManager) return;

        const active = this.speaking && this.visemeSchedule && this.visemeSchedule.length > 0 && this.visemeDuration > 0;

        // Initialize lip sync state if needed
        if (!this.lipSyncState) {
            this.lipSyncState = {
                smooth: { A: 0, E: 0, I: 0, O: 0, U: 0 },
                lastActiveAt: 0
            };
        }

        if (!active) {
            // Smoothstep release when speech ends (Airi: useMotionUpdatePluginLipSync)
            if (this.lipSyncRelease.remainingMs > 0) {
                this.lipSyncRelease.remainingMs = Math.max(0, this.lipSyncRelease.remainingMs - delta * 1000);
                const blend = this._smoothstep(1 - this.lipSyncRelease.remainingMs / LIP_RELEASE_DURATION_MS);
                // Read current motion-driven mouth value and crossfade
                const motionValue = this.vrm.expressionManager.getValue('aa') || 0;
                const blended = this.lipSyncRelease.lastForcedValue * (1 - blend) + motionValue * blend;
                for (const bs of Object.values(VOWEL_MAP)) {
                    this.vrm.expressionManager.setValue(bs, blended * 0.7);
                }
            } else {
                // Fully released — fade to zero
                for (const bs of Object.values(VOWEL_MAP)) {
                    const r = 1 - Math.exp(-LIP_RELEASE * delta);
                    this.smoothedVowels[bs] += (0 - this.smoothedVowels[bs]) * r;
                    if (this.smoothedVowels[bs] < 0.004) this.smoothedVowels[bs] = 0;
                    this.vrm.expressionManager.setValue(bs, this.smoothedVowels[bs]);
                }
            }
            return;
        }

        // Track last forced value for smooth release
        this.lipSyncRelease.remainingMs = LIP_RELEASE_DURATION_MS;

        const playhead = this.audioPlayer ? this.audioPlayer.getPlayhead() : 0;
        const phase = clamp(playhead / this.visemeDuration, 0, 1);
        const seg = visemeSegmentAt(this.visemeSchedule, phase);

        // Real voice amplitude — non-linear perceptual response curve
        const rms = this._computeRMS();
        // 0.55 exponent: more responsive at low volumes (whisper visible),
        // 1.2 multiplier: compensates at high volumes for natural emphasis
        const amp = Math.pow(Math.min(rms * 1.2, 1), 0.55);
        const gate = energyGate(rms, LIP_SILENCE_VOL, 0.10);

        // Map current viseme to vowel
        const currentVowel = seg ? Object.keys(VOWEL_MAP).find(k => VOWEL_MAP[k] === seg.viseme) : null;

        // Project all vowel weights based on current viseme and amplitude
        const projected = { A: 0, E: 0, I: 0, O: 0, U: 0 };
        if (currentVowel && seg) {
            projected[currentVowel] = Math.max(projected[currentVowel], seg.open * amp);
        }

        // Winner + runner selection (only top 2 vowels, not all)
        let winner = 'I', runner = 'E';
        let winnerVal = -Infinity, runnerVal = -Infinity;
        for (const key of ['A', 'E', 'I', 'O', 'U']) {
            const val = projected[key];
            if (val > winnerVal) {
                runnerVal = winnerVal;
                runner = winner;
                winnerVal = val;
                winner = key;
            } else if (val > runnerVal) {
                runnerVal = val;
                runner = key;
            }
        }

        // Detect silence/pause
        const now = performance.now();
        let silent = amp < LIP_SILENCE_VOL || winnerVal < LIP_SILENCE_GAIN;
        if (!silent) this.lipSyncState.lastActiveAt = now;
        if (now - this.lipSyncState.lastActiveAt > LIP_IDLE_MS) silent = true;

        // Calculate target weights for winner and runner
        const target = { A: 0, E: 0, I: 0, O: 0, U: 0 };
        if (!silent) {
            target[winner] = Math.min(LIP_CAP, winnerVal);
            target[runner] = Math.min(LIP_CAP * 0.5, runnerVal * 0.6);
        }

        // Smooth transitions with attack/release
        let maxWeight = 0;
        for (const key of ['A', 'E', 'I', 'O', 'U']) {
            const bs = VOWEL_MAP[key];
            const from = this.smoothedVowels[bs];
            const to = target[key];
            const rate = 1 - Math.exp(-(to > from ? LIP_ATTACK : LIP_RELEASE) * delta);
            this.smoothedVowels[bs] = from + (to - from) * rate;
            const weight = (this.smoothedVowels[bs] <= 0.01 ? 0 : this.smoothedVowels[bs]) * 0.7;
            this.vrm.expressionManager.setValue(bs, weight);
            if (weight > maxWeight) maxWeight = weight;
        }
        // Track last forced value for smoothstep release
        this.lipSyncRelease.lastForcedValue = maxWeight;
    }

    // True 0..1 amplitude from the live audio waveform (time domain).
    _computeRMS() {
        if (!this.analyser) return 0;
        if (!this.timeData || this.timeData.length !== this.analyser.fftSize) {
            this.timeData = new Uint8Array(this.analyser.fftSize);
        }
        this.analyser.getByteTimeDomainData(this.timeData);
        let sum = 0;
        for (let i = 0; i < this.timeData.length; i++) {
            const v = (this.timeData[i] - 128) / 128;
            sum += v * v;
        }
        return Math.sqrt(sum / this.timeData.length);
    }

    // ── Natural Blink System with partial blinks and smoother curves ──
    _updateBlink(delta) {
        if (!this.vrm?.expressionManager) return;
        const dtMs = delta * 1000;

        // Initialize blink state if needed
        if (!this._blinkState) {
            this._blinkState = {
                phase: 'idle', // idle | closing | opening
                progress: 0,
                startLeft: 1, startRight: 1,
                delayMs: this._rand(BLINK_DELAY_MIN, BLINK_DELAY_MAX) * 1000,
                openDurationMs: this._rand(BLINK_OPEN_MIN, BLINK_OPEN_MAX) * 1000,
                isPartial: false,
                partialIntensity: 1,
            };
        }
        const bs = this._blinkState;

        // Natural ease curves using sine for smoother motion
        const easeOutSine = (t) => Math.sin((t * Math.PI) / 2);
        const easeInSine = (t) => 1 - Math.cos((t * Math.PI) / 2);
        const clamp01 = (v) => Math.min(1, Math.max(0, v));

        // Get current eye openness as base
        const baseLeft = clamp01(this.vrm.expressionManager.getValue('eyeLOpen') ?? 1);
        const baseRight = clamp01(this.vrm.expressionManager.getValue('eyeROpen') ?? 1);

        // Skip blink if eyes are already nearly closed
        if (bs.phase === 'idle' && baseLeft <= BLINK_SKIP_THRESHOLD && baseRight <= BLINK_SKIP_THRESHOLD) {
            bs.delayMs = this._rand(BLINK_DELAY_MIN, BLINK_DELAY_MAX) * 1000;
            return;
        }

        // Idle: count down delay to next blink
        if (bs.phase === 'idle') {
            bs.delayMs -= dtMs;
            if (bs.delayMs <= 0) {
                bs.phase = 'closing';
                bs.progress = 0;
                bs.startLeft = baseLeft;
                bs.startRight = baseRight;
                // Determine if this is a partial blink (more natural)
                bs.isPartial = secureRand() < PARTIAL_BLINK_CHANCE;
                bs.partialIntensity = bs.isPartial ? 0.4 + secureRand() * 0.3 : 1; // 40-70% for partial
            }
            return;
        }

        // Closing: move toward zero with smooth sine curve
        if (bs.phase === 'closing') {
            bs.progress = Math.min(1, bs.progress + dtMs / (BLINK_CLOSE_DURATION * 1000));
            const eased = easeOutSine(bs.progress);
            const closeAmount = bs.partialIntensity * eased; // Partial blinks don't close fully
            const eyeL = clamp01(bs.startLeft * (1 - closeAmount));
            const eyeR = clamp01(bs.startRight * (1 - closeAmount));
            this.vrm.expressionManager.setValue('eyeLOpen', eyeL);
            this.vrm.expressionManager.setValue('eyeROpen', eyeR);
            // Also set blink expression for models that use it
            this.vrm.expressionManager.setValue('blink', Math.sin(bs.progress * Math.PI) * bs.partialIntensity);
            if (bs.progress >= 1) {
                bs.phase = 'opening';
                bs.progress = 0;
                bs.openDurationMs = this._rand(BLINK_OPEN_MIN, BLINK_OPEN_MAX) * 1000;
            }
            return;
        }

        // Opening: move back to base with smooth sine curve
        bs.progress = Math.min(1, bs.progress + dtMs / bs.openDurationMs);
        const eased = easeInSine(bs.progress);
        const closeAmount = bs.partialIntensity * (1 - eased); // Fade from partial to open
        const eyeL = clamp01(bs.startLeft * eased);
        const eyeR = clamp01(bs.startRight * eased);
        this.vrm.expressionManager.setValue('eyeLOpen', eyeL);
        this.vrm.expressionManager.setValue('eyeROpen', eyeR);
        this.vrm.expressionManager.setValue('blink', Math.sin((1 - bs.progress) * Math.PI) * bs.partialIntensity);

        if (bs.progress >= 1) {
            // Blink complete — reset to idle
            bs.phase = 'idle';
            bs.progress = 0;
            bs.delayMs = this._rand(BLINK_DELAY_MIN, BLINK_DELAY_MAX) * 1000;
            // Double blink chance (less likely after partial blink)
            if (!bs.isPartial && secureRand() < DOUBLE_BLINK_CHANCE) {
                bs.delayMs = 120; // Quick second blink
            }
        }
    }

    // ── Natural gaze: CPT saccades + mouse tracking (Airi-style) ──
    _updateGaze(delta) {
        if (!this.vrm?.lookAt) return;

        this.gazeTimer += delta;
        if (this.gazeTimer >= this.nextGazeShift) {
            this.gazeTimer = 0;
            this.nextGazeShift = this._cptSaccadeInterval(); // CPT-distributed

            if (secureRand() < WANDER_CHANCE) {
                // Look around the room (toward desk/books), then back.
                const side = secureRand() < 0.5 ? -1 : 1;
                this.gazePoint.set(
                    this.defaultLookAt.x + side * this._rand(0.4, 0.9),
                    this.defaultLookAt.y + this._rand(-0.1, 0.3),
                    this.defaultLookAt.z
                );
                this.gazeWander = 1;
            } else {
                // Small natural saccade near the user.
                this.gazePoint.set(
                    this.defaultLookAt.x + (secureRand() - 0.5) * 0.12,
                    this.defaultLookAt.y + (secureRand() - 0.5) * 0.08,
                    this.defaultLookAt.z
                );
                this.gazeWander = 0;
            }
        }

        // Mouse tracking: project screen cursor to 3D gaze plane (Airi: useLive2DEyeFocusFor)
        if (this.userPresent && this.gazeWander < 0.5 && this.camera) {
            // Project mouse position onto the eye-height plane
            const mouseScreen = new THREE.Vector2(this.mouseX, this.mouseY);
            const raycaster = new THREE.Raycaster();
            raycaster.setFromCamera(mouseScreen, this.camera);
            // Intersect with horizontal plane at eye height
            const plane = new THREE.Plane(new THREE.Vector3(0, 0, -1), -this.defaultLookAt.z);
            const intersect = new THREE.Vector3();
            raycaster.ray.intersectPlane(plane, intersect);
            if (intersect) {
                // Blend mouse influence (stronger when closer)
                const influence = 0.25;
                this.gazePoint.x = lerp(this.gazePoint.x, intersect.x * influence + this.defaultLookAt.x * (1 - influence), 0.08);
                this.gazePoint.y = lerp(this.gazePoint.y, intersect.y * influence + this.defaultLookAt.y * (1 - influence), 0.08);
            }
        }

        // If the user is typing, glance toward where they are (camera-left of her view).
        if (this.userTyping && this.gazeWander < 0.5) {
            this.gazePoint.x = lerp(this.gazePoint.x, this.defaultLookAt.x - 0.35, 0.08);
            this.gazePoint.y = lerp(this.gazePoint.y, this.defaultLookAt.y - 0.05, 0.08);
        }

        // Ease the fixation target toward the chosen gaze point.
        const lerpSpeed = 1 - Math.exp(-12 * delta);
        this.fixationTarget.lerp(this.gazePoint, lerpSpeed);
        // Tiny constant micro-drift so the eyes never feel frozen.
        this.fixationTarget.x += Math.sin(performance.now() * 0.0007) * 0.004;
        this.fixationTarget.y += Math.sin(performance.now() * 0.0009 + 1.3) * 0.003;

        if (this.vrm.lookAt.target) {
            this.vrm.lookAt.target.position.copy(this.fixationTarget);
            this.vrm.lookAt.update(delta);
        }

        // Update spring-damper head tracking toward mouse
        this._updateHeadSpring(delta);
    }

    // Spring-damper head physics (Airi: useMotionUpdatePluginBeatSync)
    _updateHeadSpring(delta) {
        if (!this.vrm?.humanoid) return;

        // Compute target head angles from mouse position
        const targetYaw = this.mouseX * 0.08;
        const targetPitch = this.mouseY * 0.04;
        const targetRoll = this.mouseX * 0.02;

        const s = this.headSpring;
        // Spring-damper on each axis
        const pitchResult = this._springDamper(s.posPitch, targetPitch, s.velPitch, delta);
        s.posPitch = pitchResult.pos; s.velPitch = pitchResult.vel;
        const yawResult = this._springDamper(s.posYaw, targetYaw, s.velYaw, delta);
        s.posYaw = yawResult.pos; s.velYaw = yawResult.vel;
        const rollResult = this._springDamper(s.posRoll, targetRoll, s.velRoll, delta);
        s.posRoll = rollResult.pos; s.velRoll = rollResult.vel;

        // Apply spring-damper offset on top of existing head rotation
        const head = this.vrm.humanoid.getNormalizedBoneNode('head');
        if (head) {
            head.rotation.x += s.posPitch;
            head.rotation.y += s.posYaw;
            head.rotation.z += s.posRoll;
        }
        const neck = this.vrm.humanoid.getNormalizedBoneNode('neck');
        if (neck) {
            neck.rotation.x += s.posPitch * 0.4;
            neck.rotation.y += s.posYaw * 0.4;
            neck.rotation.z += s.posRoll * 0.3;
        }
    }

    // ── Expressions with Airi-style smooth easing ──
    _ease(t) {
        // Airi's easeInOutCubic for smoother transitions
        return t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;
    }

    _clampIntensity(value) {
        return Math.min(1, Math.max(0, value));
    }

    _setEmotion(name, intensity = 1) {
        if (!this.vrm?.expressionManager) return;
        clearTimeout(this.expressionResetTimer);
        if (!Object.hasOwn(EMOTION_MAP, name)) return;
        const st = EMOTION_MAP[name];
        this.currentEmotion = name;
        this.isTransitioning = true;
        this.transitionProgress = 0;

        // Airi-style: Capture current values BEFORE resetting for smooth transition
        this.currentExpressionValues.clear();
        this.targetExpressionValues.clear();
        const map = this.vrm.expressionManager.expressionMap || this.vrm.expressionManager._expressionMap;
        if (map) {
            for (const n of Object.keys(map)) {
                const currentValue = this.vrm.expressionManager.getValue(n) || 0;
                this.currentExpressionValues.set(n, currentValue);
                this.targetExpressionValues.set(n, 0); // Default target is 0
            }
        }

        const norm = this._clampIntensity(intensity);
        
        // Use facial expressions JSON if available
        if (st.facialKey && this.facialExpressions && this.facialExpressions[st.facialKey]) {
            const facialData = this.facialExpressions[st.facialKey];
            if (facialData.morphTargets) {
                for (const [morphName, value] of Object.entries(facialData.morphTargets)) {
                    this.targetExpressionValues.set(morphName, value * norm);
                }
            }
        }
        
        // Fallback to standard expressions
        for (const e of st.expression || []) {
            this.targetExpressionValues.set(e.name, e.value * norm);
        }
    }

    _updateExpressions(delta) {
        if (!this.isTransitioning || !this.currentEmotion || !this.vrm?.expressionManager) return;
        const dur = EMOTION_MAP[this.currentEmotion]?.blendDuration || 0.3;
        this.transitionProgress += delta / dur;
        if (this.transitionProgress >= 1) { this.transitionProgress = 1; this.isTransitioning = false; }
        
        // Airi-style: Use easeInOutCubic for all expression transitions
        const eased = this._ease(this.transitionProgress);
        for (const [n, t] of this.targetExpressionValues) {
            const s = this.currentExpressionValues.get(n) || 0;
            const currentValue = s + (t - s) * eased;
            this.vrm.expressionManager.setValue(n, currentValue);
        }
    }

    _updateMicroExpressions(delta) {
        if (!this.vrm?.expressionManager) return;
        if (this.speaking || this.isTransitioning || this.restMode) return;

        this.microExpressionTimer += delta;
        
        // Trigger new micro-expression
        if (!this.microExpressionActive && this.microExpressionTimer >= this.nextMicroExpression) {
            this.microExpressionTimer = 0;
            this.nextMicroExpression = 3 + secureRand() * 5;
            this.microExpressionActive = true;
            this.microExpressionDuration = 0.8 + secureRand() * 0.6;
            this.microExpressionProgress = 0;
            
            // Airi-style: varied micro-expressions with subtle intensity
            const microOptions = [
                { expressions: { happy: 0.08 + secureRand() * 0.06 }, duration: 0.8 },
                { expressions: { neutral: 0.90 + secureRand() * 0.08 }, duration: 1.0 },
                { expressions: { surprised: 0.05 + secureRand() * 0.04 }, duration: 0.5 },
                { expressions: { blink: 0.15 + secureRand() * 0.10 }, duration: 0.3 },
                { expressions: { aa: 0.03 + secureRand() * 0.02 }, duration: 0.4 }, // Slight mouth movement
                { expressions: { oh: 0.04 + secureRand() * 0.03 }, duration: 0.5 },
            ];
            const chosen = microOptions[Math.floor(secureRand() * microOptions.length)];
            this.microExpressionTargets = chosen.expressions;
            this.microExpressionDuration = chosen.duration;
        }

        // Update active micro-expression with smooth in-out
        if (this.microExpressionActive) {
            this.microExpressionProgress += delta / this.microExpressionDuration;
            
            // Smooth in-out curve (sine-based)
            const curve = Math.sin(Math.min(1, this.microExpressionProgress) * Math.PI);
            
            for (const [name, target] of Object.entries(this.microExpressionTargets)) {
                const current = this.microExpressionCurrent[name] || 0;
                const speed = 1 - Math.exp(-3 * delta);
                const targetWithCurve = target * curve;
                this.microExpressionCurrent[name] = current + (targetWithCurve - current) * speed;
                
                // Apply micro-expression on top of existing expressions
                const existing = this.vrm.expressionManager.getValue(name) || 0;
                // Only add if it won't conflict with major expressions
                if (existing < 0.2 || name === 'blink') {
                    this.vrm.expressionManager.setValue(name, this.microExpressionCurrent[name]);
                }
            }
            
            // End micro-expression
            if (this.microExpressionProgress >= 1) {
                this.microExpressionActive = false;
                this.microExpressionProgress = 0;
                // Fade out
                for (const name of Object.keys(this.microExpressionTargets)) {
                    this.microExpressionCurrent[name] = 0;
                }
            }
        }
    }

    // ── Spontaneous Smiling System ──
    _updateSpontaneousSmile(delta) {
        if (!this.vrm?.expressionManager) return;
        if (this.speaking || this.isTransitioning || !this.userPresent) return;

        this.spontaneousSmileTimer += delta;

        // Check if it's time for potential smile
        if (!this.spontaneousSmileActive && this.spontaneousSmileTimer >= this.nextSmileTime) {
            this.spontaneousSmileTimer = 0;
            this.nextSmileTime = 8 + secureRand() * 12; // Reset timer for next check

            // Only smile 15% of the time (natural, not forced)
            if (secureRand() < SPONTANEOUS_SMILE_CHANCE) {
                this.spontaneousSmileActive = true;
                this.spontaneousSmileDuration = 2 + secureRand() * 2; // 2-4 seconds
                this.spontaneousSmileProgress = 0;
                this.spontaneousSmileIntensity = 0.1 + secureRand() * 0.2; // Subtle: 0.1-0.3
            }
        }

        // Update active smile with smooth fade in-out
        if (this.spontaneousSmileActive) {
            this.spontaneousSmileProgress += delta / this.spontaneousSmileDuration;

            // Smooth in-out curve (sine-based for natural feel)
            const curve = Math.sin(Math.min(1, this.spontaneousSmileProgress) * Math.PI);
            const smileValue = this.spontaneousSmileIntensity * curve;

            // Apply smile if it won't conflict with major emotions
            const existingHappy = this.vrm.expressionManager.getValue('happy') || 0;
            if (existingHappy < 0.3) {
                this.vrm.expressionManager.setValue('happy', smileValue);
            }

            // End smile
            if (this.spontaneousSmileProgress >= 1) {
                this.spontaneousSmileActive = false;
                this.spontaneousSmileProgress = 0;
                // Fade out
                if (existingHappy < 0.3) {
                    this.vrm.expressionManager.setValue('happy', 0);
                }
            }
        }
    }

    // ── Enhanced Emotion posture + idle behaviors + rest state (Airi-style) ──
    _updateLife(delta, elapsed) {
        if (!this.vrm?.humanoid) return;
        const h = this.vrm.humanoid;

        // Decide rest mode from time since interaction.
        const since = (performance.now() - this.lastInteraction) / 1000;
        const wasRest = this.restMode;
        this.restMode = since > REST_IDLE_SECONDS;
        if (this.restMode !== wasRest) {
            this.breathScale = this.restMode ? 0.5 : 1.0; // Deeper rest mode breathing
        }
        if (!this.restMode) this.breathScale = lerp(this.breathScale, 1.0, 1 - Math.exp(-2 * delta));

        // Enhanced emotion-driven head posture targets with more nuance.
        let tp = 0, ty = 0, tr = 0;
        switch (this.currentEmotion) {
            case 'happy': tp = -0.06; tr = 0.05; ty = 0.02; break;
            case 'sad': tp = 0.07; tr = -0.03; ty = -0.02; break;
            case 'surprised': tp = -0.08; ty = 0.03; break;
            case 'excited': tp = -0.05; tr = 0.04; ty = 0.02; break;
            case 'frustrated':
            case 'angry': tp = 0.03; tr = -0.06; ty = 0.01; break;
            case 'think': ty = 0.07; tp = -0.04; tr = 0.06; break;
            case 'calm': tp = 0.01; tr = 0.01; break;
        }
        if (this.agentStatus === 'thinking') { ty = 0.06; tp = -0.04; tr = 0.06; }
        if (this.restMode) { tp = lerp(tp, 0.06, 0.7); ty = lerp(ty, 0.02, 0.5); }

        // Enhanced idle behaviors with more variety
        if (!this.speaking) {
            this.idleBehaviorTimer -= delta;
            if (!this.idleBehavior && this.idleBehaviorTimer <= 0) {
                this.idleBehaviorTimer = this._rand(IDLE_BEHAVIOR_MIN, IDLE_BEHAVIOR_MAX);
                const behaviorOptions = ['stretch', 'glance', 'adjust', 'breatheDeep', 'headNod', 'shoulderShrug'];
                const kind = behaviorOptions[Math.floor(secureRand() * behaviorOptions.length)];
                const durations = { stretch: 2.2, glance: 1.4, adjust: 1.0, breatheDeep: 2.8, headNod: 0.8, shoulderShrug: 1.2 };
                this.idleBehavior = { kind, dur: durations[kind] };
                this.idleBehaviorT = 0;
            }
            if (this.idleBehavior) {
                this.idleBehaviorT += delta;
                const k = this.idleBehavior.kind;
                const p = this.idleBehaviorT / this.idleBehavior.dur;
                const env = Math.sin(Math.min(1, p) * Math.PI); // 0→1→0
                
                switch (k) {
                    case 'stretch': 
                        tr += env * 0.07; tp -= env * 0.04; ty += env * 0.02; break;
                    case 'glance': 
                        ty += env * 0.15; tp += env * 0.02; break;
                    case 'adjust': 
                        tr += env * 0.03; tp -= env * 0.02; break;
                    case 'breatheDeep':
                        this.breathScale = lerp(this.breathScale, 1.3, env * 0.5); break;
                    case 'headNod':
                        tp += env * 0.04; // Subtle nod down
                        break;
                    case 'shoulderShrug':
                        tr += env * 0.02; // Slight shoulder roll
                        break;
                }
                if (p >= 1) this.idleBehavior = null;
            }
        }

        // Head nod when user types (acknowledgment)
        if (this.userTyping && !this.speaking) {
            const nodIntensity = Math.min((performance.now() - this.lastInteraction) / 1000, 1);
            tp += Math.sin(elapsed * 8) * 0.02 * nodIntensity;
        }

        // Smooth toward posture targets with slightly faster response.
        const ps = 1 - Math.exp(-8 * delta);
        this.postureHead.pitch = lerp(this.postureHead.pitch, tp, ps);
        this.postureHead.yaw = lerp(this.postureHead.yaw, ty, ps);
        this.postureHead.roll = lerp(this.postureHead.roll, tr, ps);

        const head = h.getNormalizedBoneNode('head');
        const neck = h.getNormalizedBoneNode('neck');
        if (head) {
            head.rotation.x += this.postureHead.pitch;
            head.rotation.y += this.postureHead.yaw;
            head.rotation.z += this.postureHead.roll;
        }
        if (neck) {
            neck.rotation.x += this.postureHead.pitch * 0.4;
            neck.rotation.y += this.postureHead.yaw * 0.4;
            neck.rotation.z += this.postureHead.roll * 0.4;
        }

        // Enhanced stretch with more natural arm movement.
        if (this.idleBehavior && this.idleBehavior.kind === 'stretch') {
            const p = Math.min(1, this.idleBehaviorT / this.idleBehavior.dur);
            const env = Math.sin(p * Math.PI) * 0.6;
            const lu = h.getNormalizedBoneNode('leftUpperArm');
            const ru = h.getNormalizedBoneNode('rightUpperArm');
            if (lu) {
                lu.rotation.z += env * 0.45;
                lu.rotation.x -= env * 0.1;
            }
            if (ru) {
                ru.rotation.z -= env * 0.45;
                ru.rotation.x -= env * 0.1;
            }
        }
    }

    // ── Public API ──
    setEmotion(emotion, intensity) {
        this._setEmotion(emotion || 'calm', intensity || 0.5);
        clearTimeout(this.expressionResetTimer);
        this.expressionResetTimer = setTimeout(() => this._setEmotion('neutral', 1), EXPRESSION_RESET_MS);
        this.lastInteraction = performance.now();
    }

    prepareVisemes(text) {
        const sched = buildVisemeSchedule(text);
        this.visemeSchedule = sched.segments;
        // Store for sentence pitch contour detection
        this._lastUtteranceText = text;
        this._lastUtteranceEndsWithQuestion = /[?]$/.test(text.trim()) || /[？]$/.test(text.trim());
    }

    setVisemeDuration(d) {
        if (d > 0) this.visemeDuration = d;
    }

    setSpeaking(s) {
        this.speaking = s;
        if (s) {
            this.lastInteraction = performance.now();
            this.lipSyncRelease.remainingMs = 0; // Cancel any pending release
        }
        if (!s) {
            this.visemeSchedule = null;
            this.visemeDuration = 0;
            // Don't reset vowels immediately — let smoothstep release handle fade-out
        }
    }

    setStatus(status) {
        this.agentStatus = status || 'idle';
        if (status !== 'idle') this.lastInteraction = performance.now();
    }

    setUserTyping(b) {
        this.userTyping = !!b;
        if (b) this.lastInteraction = performance.now();
    }

    notifyUserPresent() {
        this.userPresent = true;
        this.lastInteraction = performance.now();
    }

    // ── Enhanced Organic Idle Animation (Airi-style natural movement) ──
    _updateIdle(elapsed, delta) {
        if (!this.vrm?.humanoid) return;
        const h = this.vrm.humanoid;

        const setIdleRotation = (boneName, x, y, z) => {
            const node = h.getNormalizedBoneNode(boneName);
            if (!node) return;
            node.rotation.x = x;
            node.rotation.y = y;
            node.rotation.z = z;
        };

        // Initialize breathing phase if needed
        if (!this.breathPhase) {
            this.breathPhase = secureRand() * Math.PI * 2;
        }

        // Natural body sway with multiple frequency layers for organic feel
        const sway = Math.sin(elapsed * 0.12) * Math.sin(elapsed * 0.20) + Math.sin(elapsed * 0.08) * 0.3;
        const hipsNode = h.getNormalizedBoneNode('hips');
        if (hipsNode) {
            if (this.hipsBaseX === null) { this.hipsBaseX = hipsNode.position.x; this.hipsBaseY = hipsNode.position.y; this.hipsBaseZ = hipsNode.position.z; }
            hipsNode.position.x = this.hipsBaseX + sway * 0.006;
            hipsNode.rotation.z = sway * 0.004;
        }

        // Enhanced breathing with natural variance and rest mode scaling
        const b = this.breathScale;
        const breathRate = BREATH_BASE_RATE + (this.restMode ? -0.15 : 0);
        const breath = Math.sin(elapsed * breathRate + this.breathPhase);
        const breath2 = Math.sin(elapsed * breathRate * 1.3 + this.breathPhase + 0.35);
        const breath3 = Math.sin(elapsed * breathRate * 0.7 + this.breathPhase + 0.7);
        
        // Multi-layered breathing for realism (increased amplitude by 20%)
        const breathAmp = 1.2; // 20% more visible breathing
        setIdleRotation('spine', (breath * 0.0084 + breath2 * 0.0024 + breath3 * 0.0012) * b * breathAmp, 0, Math.sin(elapsed * 0.12) * 0.001 * b);
        setIdleRotation('chest', (breath * 0.006 + breath2 * 0.0012) * b * breathAmp, 0, 0);
        
        // Subtle shoulder breathing
        const leftShoulder = h.getNormalizedBoneNode('leftShoulder');
        const rightShoulder = h.getNormalizedBoneNode('rightShoulder');
        if (leftShoulder) leftShoulder.rotation.x += (breath * 0.002) * b;
        if (rightShoulder) rightShoulder.rotation.x += (breath * 0.002) * b;

        // More natural head movement with layered frequencies
        const t = elapsed;
        const headPitch = Math.sin(t * 0.25 + 0.6) * 0.004 + Math.sin(t * 0.5) * 0.002 + Math.sin(t * 0.08) * 0.001;
        const headYaw = Math.sin(t * 0.18) * 0.006 + Math.sin(t * 0.35) * 0.003 + Math.sin(t * 0.12) * 0.001;
        const headRoll = Math.sin(t * 0.15 + 1.1) * 0.003 + Math.sin(t * 0.22) * 0.001;
        setIdleRotation('head', clamp(headPitch, -0.14, 0.14), clamp(headYaw, -0.21, 0.21), clamp(headRoll, -0.09, 0.09));
        setIdleRotation('neck', clamp(headPitch * 0.4, -0.08, 0.08), clamp(headYaw * 0.4, -0.12, 0.12), clamp(headRoll * 0.3, -0.05, 0.05));

        // More natural arm sway with individual variation
        const leftArmZ = -1.25 + Math.sin(elapsed * 0.15 + 0.5) * 0.006 + Math.sin(elapsed * 0.08) * 0.003;
        const rightArmZ = 1.25 - Math.sin(elapsed * 0.15 + 0.3) * 0.006 - Math.sin(elapsed * 0.09) * 0.003;
        setIdleRotation('leftUpperArm', 0.05, 0.05, leftArmZ);
        setIdleRotation('rightUpperArm', 0.05, -0.05, rightArmZ);
        setIdleRotation('leftLowerArm', -0.35 + Math.sin(elapsed * 0.2 + 0.7) * 0.006, 0.1, 0);
        setIdleRotation('rightLowerArm', -0.35 - Math.sin(elapsed * 0.2 + 0.2) * 0.006, -0.1, 0);
        
        // Subtle finger movement for aliveness
        const leftHand = h.getNormalizedBoneNode('leftHand');
        const rightHand = h.getNormalizedBoneNode('rightHand');
        if (leftHand) leftHand.rotation.z += Math.sin(elapsed * 0.3) * 0.002;
        if (rightHand) rightHand.rotation.z += Math.sin(elapsed * 0.35) * 0.002;
    }

    // ── Speech Body Animation (Phase 3) ──
    // Natural head nods, breathing sync, and body movement during speech
    _updateSpeechBody(delta, elapsed) {
        if (!this.speaking || !this.vrm?.humanoid) return;
        const h = this.vrm.humanoid;

        // RMS amplitude drives speech intensity
        const rms = this._computeRMS();
        const intensity = Math.min(rms * 2, 1); // 0..1 speech intensity

        // Head nod on emphasis (high amplitude = forward nod)
        if (intensity > 0.4) {
            const nodAmount = (intensity - 0.4) * 0.05; // max 0.03 rad
            const head = h.getNormalizedBoneNode('head');
            if (head) head.rotation.x += nodAmount * Math.sin(elapsed * 6);
        }

        // Subtle head tilt on questions (detected by viseme schedule ending)
        if (this.visemeSchedule && this.visemeDuration > 0) {
            const playhead = this.audioPlayer ? this.audioPlayer.getPlayhead() : 0;
            const phase = playhead / this.visemeDuration;
            // In the last 20% of speech, check if utterance ends with question
            if (phase > 0.8 && this._lastUtteranceEndsWithQuestion) {
                const head = h.getNormalizedBoneNode('head');
                if (head) head.rotation.z += 0.015 * (phase - 0.8) * 5; // gentle tilt
            }
        }

        // Breathing increases during speech (using air to speak)
        // Already handled by _updateIdle's breathScale — just boost it
        this.breathScale = Math.max(this.breathScale, 1.2);

        // Subtle shoulder lift during louder passages
        if (intensity > 0.3) {
            const lift = (intensity - 0.3) * 0.015;
            const leftShoulder = h.getNormalizedBoneNode('leftShoulder');
            const rightShoulder = h.getNormalizedBoneNode('rightShoulder');
            if (leftShoulder) leftShoulder.rotation.x += lift;
            if (rightShoulder) rightShoulder.rotation.x += lift;
        }

        // Hand gesture during expressive moments (amplitude spikes)
        if (intensity > 0.6) {
            const gesture = Math.sin(elapsed * 4) * 0.015 * (intensity - 0.6);
            const leftHand = h.getNormalizedBoneNode('leftHand');
            const rightHand = h.getNormalizedBoneNode('rightHand');
            if (leftHand) leftHand.rotation.z += gesture;
            if (rightHand) rightHand.rotation.z -= gesture;
        }
    }

    // ── Sentence Pitch Contour (Phase 5) ──
    // Adds natural intonation to head movement based on sentence type
    _updateSentencePitch(delta) {
        if (!this.speaking || !this.vrm?.humanoid) return;

        const playhead = this.audioPlayer ? this.audioPlayer.getPlayhead() : 0;
        const phase = this.visemeDuration > 0 ? playhead / this.visemeDuration : 0;

        // Detect sentence type from last prepared text
        const text = this._lastUtteranceText || '';
        const endsWithQuestion = /[?]$/.test(text.trim()) || /[？]$/.test(text.trim());
        const endsWithExclamation = /[!]$/.test(text.trim()) || /[！]$/.test(text.trim());

        if (endsWithQuestion) {
            // Questions: head tilts UP at the end
            const rise = Math.max(0, phase - 0.7) / 0.3; // 0..1 in last 30%
            const head = this.vrm.humanoid.getNormalizedBoneNode('head');
            if (head) head.rotation.x -= rise * 0.025; // pitch up (negative = up)
        } else if (endsWithExclamation) {
            // Exclamations: head lifts with energy
            const lift = Math.max(0, phase - 0.8) / 0.2;
            const head = this.vrm.humanoid.getNormalizedBoneNode('head');
            if (head) head.rotation.x -= lift * 0.02;
        } else {
            // Statements: head nods down at the end
            const drop = Math.max(0, phase - 0.8) / 0.2;
            const head = this.vrm.humanoid.getNormalizedBoneNode('head');
            if (head) head.rotation.x += drop * 0.015;
        }
    }

    _animate() {
        requestAnimationFrame(() => this._animate());
        if (!this.renderer) return; // Wait for async _init to complete
        const delta = Math.min(this.clock.getDelta(), 0.1);
        const elapsed = this.clock.getElapsedTime();

        if (this.vrm) {
            if (this.mixer) this.mixer.update(delta);
            this._updateIdle(elapsed, delta);
            this._updateLife(delta, elapsed);     // emotion posture + idle + rest (after idle)
            this._updateGaze(delta);              // gaze last so it owns the lookAt target

            this.vrm.humanoid?.update();
            // Reduce spring bone update frequency for performance
            if (Math.floor(elapsed * 60) % 2 === 0) { // Update at 30fps instead of 60fps
                this.vrm.springBoneManager?.update(delta * 2);
            }

            this._updateBlink(delta);
            this._updateExpressions(delta);
            this._updateMicroExpressions(delta);
            this._updateSpontaneousSmile(delta);
            this._updateLipSync(delta);
            this._updateSpeechBody(delta, elapsed);
            this._updateSentencePitch(delta);
            this._updateMotion3(delta);

            this.vrm.expressionManager?.update();
            this.vrm.nodeConstraintManager?.update();
        }

        this.renderer.render(this.scene, this.camera);
    }
}

window.CharacterRenderer = CharacterRenderer;
