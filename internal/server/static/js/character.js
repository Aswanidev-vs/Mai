import * as THREE from 'three';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';
import { VRMLoaderPlugin, VRMUtils } from '@pixiv/three-vrm';
import { VRMAnimationLoaderPlugin, createVRMAnimationClip } from '@pixiv/three-vrm-animation';

const CAMERA_FOV = 30;
const BLINK_DURATION = 0.15;
const MIN_BLINK_INTERVAL = 1.5;
const MAX_BLINK_INTERVAL = 5;
const DOUBLE_BLINK_CHANCE = 0.22;
// Advanced lip sync constants (from Airi)
const LIP_ATTACK = 50;
const LIP_RELEASE = 30;
const LIP_CAP = 0.7;
const LIP_SILENCE_VOL = 0.04;
const LIP_SILENCE_GAIN = 0.05;
const LIP_IDLE_MS = 160;
const EXPRESSION_RESET_MS = 4000;

// "Alive" tuning (Airi-inspired)
const REST_IDLE_SECONDS = 25;       // time before she slips into a calmer "rest" state
const GAZE_SHIFT_MIN = 2.0;         // seconds between micro-saccades
const GAZE_SHIFT_MAX = 5.0;
const WANDER_CHANCE = 0.30;         // chance a gaze shift is a longer "look around the room"
const IDLE_BEHAVIOR_MIN = 10;       // seconds between occasional idle behaviors (more frequent)
const IDLE_BEHAVIOR_MAX = 25;
const BREATH_BASE_RATE = 0.75;      // Base breathing rate (slower = more relaxed)
const BREATH_VARIANCE = 0.15;       // Random variance in breathing
const SPONTANEOUS_SMILE_CHANCE = 0.15; // 15% chance to smile during idle check

const EMOTION_MAP = {
    happy:     { expression: [{ name: 'happy', value: 0.7 }], blendDuration: 0.4 },
    sad:       { expression: [{ name: 'sad', value: 0.65 }], blendDuration: 0.5 },
    angry:     { expression: [{ name: 'angry', value: 0.65 }], blendDuration: 0.3 },
    surprised: { expression: [{ name: 'surprised', value: 0.75 }], blendDuration: 0.15 },
    neutral:   { expression: [{ name: 'neutral', value: 1.0 }], blendDuration: 0.6 },
    think:     { expression: [{ name: 'think', value: 0.6 }], blendDuration: 0.5 },
    calm:      { expression: [{ name: 'neutral', value: 1.0 }], blendDuration: 0.7 },
    stressed:  { expression: [{ name: 'angry', value: 0.5 }], blendDuration: 0.35 },
    excited:   { expression: [{ name: 'surprised', value: 0.6 }, { name: 'happy', value: 0.3 }], blendDuration: 0.2 },
    frustrated:{ expression: [{ name: 'angry', value: 0.55 }], blendDuration: 0.35 },
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

        // Blink
        this.isBlinking = false;
        this.blinkProgress = 0;
        this.timeSinceLastBlink = 0;
        this.nextBlinkTime = MIN_BLINK_INTERVAL + secureRand() * (MAX_BLINK_INTERVAL - MIN_BLINK_INTERVAL);
        this.pendingDoubleBlink = false;

        // Gaze / "alive" gaze controller
        this.fixationTarget = new THREE.Vector3(0, 1.3, 0);
        this.defaultLookAt = new THREE.Vector3(0, 1.3, 0);
        this.eyeHeight = 1.3;
        this.gazePoint = new THREE.Vector3(0, 1.3, 0);   // current gaze destination
        this.nextGazeShift = this._rand(GAZE_SHIFT_MIN, GAZE_SHIFT_MAX);
        this.gazeTimer = 0;
        this.gazeWander = 0;                               // 0..1 how far she's looking away

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

        // Parallax background system
        this.parallaxLayers = [];
        this.mouseX = 0;
        this.mouseY = 0;
        this.targetMouseX = 0;
        this.targetMouseY = 0;

        // Enhanced user responsiveness
        this.headTiltTarget = 0;
        this.headTiltCurrent = 0;
        this.lastUserInteractionTime = performance.now();

        this._init();
        this._createParallaxBackground();
        this._loadModel();
        this._animate();
        this._setupParallaxEvents();
    }

    _rand(a, b) { return a + secureRand() * (b - a); }

    _init() {
        this.renderer = new THREE.WebGLRenderer({
            antialias: false, // Disabled for performance
            alpha: false,
            powerPreference: 'high-performance',
        });
        this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 1.5)); // Reduced from 2 for performance
        this.renderer.setClearColor(0x0e0c15, 1);
        this.renderer.outputColorSpace = THREE.SRGBColorSpace;
        this.renderer.toneMapping = THREE.LinearToneMapping; // Cheaper than ACESFilmic
        this.renderer.toneMappingExposure = 1.15;
        this.renderer.shadowMap.enabled = false; // Disabled - not needed for 2D background
        this.container.appendChild(this.renderer.domElement);

        this.scene = new THREE.Scene();
        // Fog removed - not needed with 2D background
        this.camera = new THREE.PerspectiveCamera(CAMERA_FOV, 1, 0.01, 100);

        this._resize();
        window.addEventListener('resize', () => this._resize());
        // ResizeObserver catches layout changes that the window resize event misses.
        if (window.ResizeObserver && this.container) {
            new ResizeObserver(() => this._resize()).observe(this.container);
        }
        // Deferred resize ensures the canvas is sized after CSS layout completes.
        requestAnimationFrame(() => this._resize());
    }

    _resize() {
        const w = this.container.clientWidth;
        const h = this.container.clientHeight;
        if (w === 0 || h === 0) return;
        this.renderer.setSize(w, h);
        this.camera.aspect = w / h;
        this.camera.updateProjectionMatrix();
    }

    // ── 2D Parallax Background System ──
    _createParallaxBackground() {
        // Background layer (furthest, slowest movement)
        const bgLayer = new THREE.Group();
        const bgMat = new THREE.MeshBasicMaterial({ color: 0x241d30 });
        const bgPlane = new THREE.Mesh(new THREE.PlaneGeometry(30, 20), bgMat);
        bgPlane.position.z = -8;
        bgLayer.add(bgPlane);
        
        // Add gradient overlay for depth
        const gradientCanvas = document.createElement('canvas');
        gradientCanvas.width = 512;
        gradientCanvas.height = 512;
        const ctx = gradientCanvas.getContext('2d');
        const gradient = ctx.createLinearGradient(0, 0, 0, 512);
        gradient.addColorStop(0, '#3a3350');
        gradient.addColorStop(0.5, '#241d30');
        gradient.addColorStop(1, '#1a1520');
        ctx.fillStyle = gradient;
        ctx.fillRect(0, 0, 512, 512);
        const gradientTexture = new THREE.CanvasTexture(gradientCanvas);
        const gradientMat = new THREE.MeshBasicMaterial({ map: gradientTexture, transparent: true });
        const gradientPlane = new THREE.Mesh(new THREE.PlaneGeometry(30, 20), gradientMat);
        gradientPlane.position.z = -7.9;
        bgLayer.add(gradientPlane);
        
        this.parallaxLayers.push({ group: bgLayer, depth: 0.02 });
        this.scene.add(bgLayer);

        // Mid-ground layer (medium movement)
        const midLayer = new THREE.Group();
        
        // Window
        const windowMat = new THREE.MeshBasicMaterial({ color: 0xbfd4e8 });
        const windowFrame = new THREE.Mesh(new THREE.PlaneGeometry(3, 4), new THREE.MeshBasicMaterial({ color: 0x2a1d12 }));
        windowFrame.position.set(2, 2, -6);
        midLayer.add(windowFrame);
        
        const windowPane = new THREE.Mesh(new THREE.PlaneGeometry(2.5, 3.5), windowMat);
        windowPane.position.set(2, 2, -5.9);
        midLayer.add(windowPane);
        
        // Wall art
        const frameMat = new THREE.MeshBasicMaterial({ color: 0x2a1d12 });
        const artColors = [0x6b8fd9, 0xd98fb0, 0x8fd9a8];
        for (let i = 0; i < 3; i++) {
            const frame = new THREE.Mesh(new THREE.PlaneGeometry(1.2, 1.5), frameMat);
            frame.position.set(-3 + i * 1.5, 1.5, -6);
            midLayer.add(frame);
            
            const art = new THREE.Mesh(new THREE.PlaneGeometry(1, 1.2), new THREE.MeshBasicMaterial({ color: artColors[i] }));
            art.position.set(-3 + i * 1.5, 1.5, -5.9);
            midLayer.add(art);
        }
        
        // Bookshelf (simplified 2D)
        const shelfMat = new THREE.MeshBasicMaterial({ color: 0x4a3320 });
        const shelf = new THREE.Mesh(new THREE.PlaneGeometry(2, 4), shelfMat);
        shelf.position.set(-5, 0, -6);
        midLayer.add(shelf);
        
        // Books on shelf
        const bookColors = [0xb53c3c, 0x3c78b5, 0x4a9d5b, 0xd9a441];
        for (let i = 0; i < 8; i++) {
            const book = new THREE.Mesh(new THREE.PlaneGeometry(0.15, 0.4 + Math.random() * 0.3), new THREE.MeshBasicMaterial({ color: bookColors[i % bookColors.length] }));
            book.position.set(-5.8 + i * 0.2, -0.5 + Math.random() * 2, -5.9);
            book.rotation.z = (Math.random() - 0.5) * 0.2;
            midLayer.add(book);
        }
        
        this.parallaxLayers.push({ group: midLayer, depth: 0.05 });
        this.scene.add(midLayer);

        // Foreground layer (closest, fastest movement)
        const fgLayer = new THREE.Group();
        
        // Desk
        const deskMat = new THREE.MeshBasicMaterial({ color: 0x6b4a2f });
        const desk = new THREE.Mesh(new THREE.PlaneGeometry(4, 0.5), deskMat);
        desk.position.set(-2, -1.5, -4);
        fgLayer.add(desk);
        
        // Lamp
        const lampMat = new THREE.MeshBasicMaterial({ color: 0xffd9a0 });
        const lamp = new THREE.Mesh(new THREE.CircleGeometry(0.3, 16), lampMat);
        lamp.position.set(-3.5, -0.8, -3.9);
        fgLayer.add(lamp);
        
        // Plant
        const plantMat = new THREE.MeshBasicMaterial({ color: 0x4a7c59 });
        const pot = new THREE.Mesh(new THREE.CircleGeometry(0.25, 16), new THREE.MeshBasicMaterial({ color: 0xdedede }));
        pot.position.set(4, -1.8, -4);
        fgLayer.add(pot);
        
        const leaves = new THREE.Mesh(new THREE.CircleGeometry(0.4, 8), plantMat);
        leaves.position.set(4, -1.3, -3.9);
        fgLayer.add(leaves);
        
        // Rug
        const rugMat = new THREE.MeshBasicMaterial({ color: 0x6b5b95 });
        const rug = new THREE.Mesh(new THREE.CircleGeometry(2, 32), rugMat);
        rug.position.set(0, -2.5, -3);
        fgLayer.add(rug);
        
        this.parallaxLayers.push({ group: fgLayer, depth: 0.1 });
        this.scene.add(fgLayer);

        // Simple lighting (no shadows needed for 2D)
        this.scene.add(new THREE.AmbientLight(0xffffff, 0.8));
    }

    _setupParallaxEvents() {
        // Track mouse movement for parallax
        this.container.addEventListener('mousemove', (e) => {
            const rect = this.container.getBoundingClientRect();
            this.targetMouseX = ((e.clientX - rect.left) / rect.width) * 2 - 1;
            this.targetMouseY = -((e.clientY - rect.top) / rect.height) * 2 + 1;
        });

        // Touch support
        this.container.addEventListener('touchmove', (e) => {
            const rect = this.container.getBoundingClientRect();
            const touch = e.touches[0];
            this.targetMouseX = ((touch.clientX - rect.left) / rect.width) * 2 - 1;
            this.targetMouseY = -((touch.clientY - rect.top) / rect.height) * 2 + 1;
        });

        // Reset on mouse leave
        this.container.addEventListener('mouseleave', () => {
            this.targetMouseX = 0;
            this.targetMouseY = 0;
        });
    }

    _updateParallax(delta) {
        // Smooth mouse movement
        const lerpSpeed = 1 - Math.exp(-10 * delta);
        this.mouseX += (this.targetMouseX - this.mouseX) * lerpSpeed;
        this.mouseY += (this.targetMouseY - this.mouseY) * lerpSpeed;

        // Apply parallax to each layer
        for (const layer of this.parallaxLayers) {
            layer.group.position.x = this.mouseX * layer.depth * 5;
            layer.group.position.y = this.mouseY * layer.depth * 3;
        }
    }

    _loadModel() {
        const loader = new GLTFLoader();
        loader.register((parser) => new VRMLoaderPlugin(parser));
        loader.register((parser) => new VRMAnimationLoaderPlugin(parser));

        Promise.all([
            new Promise((resolve) => loader.load('/assets/mai.vrm', resolve)),
            new Promise((resolve) => loader.load('/assets/idle_loop.vrma', resolve)),
        ]).then(([vrmGltf, vrmaGltf]) => {
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
        const zDist = (size.y * 0.25) / Math.tan(rad); // Closer camera for more intimate feel
        const lookY = center.y + size.y * 0.19;

        this.camera.position.set(center.x, lookY, center.z + zDist);
        this.camera.lookAt(center.x, lookY, center.z);

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

    // ── Advanced Lip Sync (Airi-style winner+runner blending) ──
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
            // Release all vowels to silence
            for (const bs of Object.values(VOWEL_MAP)) {
                const r = 1 - Math.exp(-LIP_RELEASE * delta);
                this.smoothedVowels[bs] += (0 - this.smoothedVowels[bs]) * r;
                if (this.smoothedVowels[bs] < 0.004) this.smoothedVowels[bs] = 0;
                this.vrm.expressionManager.setValue(bs, this.smoothedVowels[bs]);
            }
            return;
        }

        const playhead = this.audioPlayer ? this.audioPlayer.getPlayhead() : 0;
        const phase = clamp(playhead / this.visemeDuration, 0, 1);
        const seg = visemeSegmentAt(this.visemeSchedule, phase);

        // Real voice amplitude (time-domain RMS)
        const rms = this._computeRMS();
        const amp = Math.min(rms * 0.9, 1) ** 0.7;
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
        for (const key of ['A', 'E', 'I', 'O', 'U']) {
            const bs = VOWEL_MAP[key];
            const from = this.smoothedVowels[bs];
            const to = target[key];
            const rate = 1 - Math.exp(-(to > from ? LIP_ATTACK : LIP_RELEASE) * delta);
            this.smoothedVowels[bs] = from + (to - from) * rate;
            const weight = (this.smoothedVowels[bs] <= 0.01 ? 0 : this.smoothedVowels[bs]) * 0.7;
            this.vrm.expressionManager.setValue(bs, weight);
        }
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

    // ── Blink (with calmer rhythm in rest mode) ──
    _updateBlink(delta) {
        if (!this.vrm?.expressionManager) return;
        this.timeSinceLastBlink += delta;
        if (!this.isBlinking && this.timeSinceLastBlink >= this.nextBlinkTime) {
            this.isBlinking = true;
            this.blinkProgress = 0;
        }
        if (this.isBlinking) {
            this.blinkProgress += delta / BLINK_DURATION;
            this.vrm.expressionManager.setValue('blink', Math.sin(Math.PI * this.blinkProgress));
            if (this.blinkProgress >= 1) {
                this.isBlinking = false;
                this.timeSinceLastBlink = 0;
                this.vrm.expressionManager.setValue('blink', 0);

                const lo = this.restMode ? MIN_BLINK_INTERVAL * 2.2 : MIN_BLINK_INTERVAL;
                const hi = this.restMode ? MAX_BLINK_INTERVAL * 2.0 : MAX_BLINK_INTERVAL;
                if (this.pendingDoubleBlink) {
                    this.pendingDoubleBlink = false;
                    this.nextBlinkTime = this._rand(lo, hi);
                } else if (secureRand() < DOUBLE_BLINK_CHANCE) {
                    this.pendingDoubleBlink = true;
                    this.nextBlinkTime = 0.12;
                } else {
                    this.nextBlinkTime = this._rand(lo, hi);
                }
            }
        }
    }

    // ── Enhanced Natural gaze: more responsive to user + occasional saccades ──
    _updateGaze(delta) {
        if (!this.vrm?.lookAt) return;

        this.gazeTimer += delta;
        if (this.gazeTimer >= this.nextGazeShift) {
            this.gazeTimer = 0;
            this.nextGazeShift = this._rand(GAZE_SHIFT_MIN, GAZE_SHIFT_MAX);

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

        // Enhanced: Gaze follows mouse/parallax for more connection
        if (this.userPresent && this.gazeWander < 0.5) {
            const gazeInfluence = 0.15;
            this.gazePoint.x = lerp(this.gazePoint.x, this.defaultLookAt.x + this.mouseX * gazeInfluence, 0.05);
            this.gazePoint.y = lerp(this.gazePoint.y, this.defaultLookAt.y + this.mouseY * gazeInfluence * 0.5, 0.05);
        }

        // If the user is typing, glance toward where they are (camera-left of her view).
        if (this.userTyping && this.gazeWander < 0.5) {
            this.gazePoint.x = lerp(this.gazePoint.x, this.defaultLookAt.x - 0.35, 0.08);
            this.gazePoint.y = lerp(this.gazePoint.y, this.defaultLookAt.y - 0.05, 0.08);
        }

        // Ease the fixation target toward the chosen gaze point.
        const lerpSpeed = 1 - Math.exp(-12 * delta); // Faster response for more aliveness
        this.fixationTarget.lerp(this.gazePoint, lerpSpeed);
        // Tiny constant micro-drift so the eyes never feel frozen.
        this.fixationTarget.x += Math.sin(performance.now() * 0.0007) * 0.004;

        if (this.vrm.lookAt.target) {
            this.vrm.lookAt.target.position.copy(this.fixationTarget);
            this.vrm.lookAt.update(delta);
        }

        // Update head tilt based on user position
        this._updateHeadTilt(delta);
    }

    _updateHeadTilt(delta) {
        if (!this.vrm?.humanoid) return;
        
        // Calculate target head tilt based on mouse position
        const targetTilt = this.mouseX * 0.08; // Subtle tilt toward user
        
        // Smooth transition
        const tiltSpeed = 1 - Math.exp(-8 * delta);
        this.headTiltCurrent += (targetTilt - this.headTiltCurrent) * tiltSpeed;
        
        // Apply to head bone
        const head = this.vrm.humanoid.getNormalizedBoneNode('head');
        if (head) {
            head.rotation.y += this.headTiltCurrent * 0.02; // Very subtle
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
            for (const bs of Object.values(VOWEL_MAP)) {
                this.smoothedVowels[bs] = 0;
                if (this.vrm?.expressionManager) this.vrm.expressionManager.setValue(bs, 0);
            }
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

    _animate() {
        requestAnimationFrame(() => this._animate());
        const delta = Math.min(this.clock.getDelta(), 0.1);
        const elapsed = this.clock.getElapsedTime();

        // Update parallax background
        this._updateParallax(delta);

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

            this.vrm.expressionManager?.update();
            this.vrm.nodeConstraintManager?.update();
        }

        this.renderer.render(this.scene, this.camera);
    }
}

window.CharacterRenderer = CharacterRenderer;
