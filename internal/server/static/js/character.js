import * as THREE from 'three';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';
import { VRMLoaderPlugin, VRMUtils } from '@pixiv/three-vrm';
import { VRMAnimationLoaderPlugin, createVRMAnimationClip } from '@pixiv/three-vrm-animation';

const CAMERA_FOV = 30;
const BLINK_DURATION = 0.15;
const MIN_BLINK_INTERVAL = 1.5;
const MAX_BLINK_INTERVAL = 5;
const DOUBLE_BLINK_CHANCE = 0.25;
const LIP_ATTACK = 65;
const LIP_RELEASE = 30;
const LIP_CAP = 0.8;
const IDLE_MOUTH_TIMEOUT = 80;
const EXPRESSION_RESET_MS = 4000;

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
        this.lastAudioTime = 0;
        this.analyser = null;
        this.freqData = null;
        this.speaking = false;

        // Blink
        this.isBlinking = false;
        this.blinkProgress = 0;
        this.timeSinceLastBlink = 0;
        this.nextBlinkTime = MIN_BLINK_INTERVAL + Math.random() * (MAX_BLINK_INTERVAL - MIN_BLINK_INTERVAL);
        this.pendingDoubleBlink = false;

        // Gaze Lock
        this.fixationTarget = new THREE.Vector3(0, 1.3, 0);
        this.defaultLookAt = new THREE.Vector3(0, 1.3, 0);
        this.eyeHeight = 1.3;

        // Expression
        this.currentExpressionValues = new Map();
        this.targetExpressionValues = new Map();
        this.isTransitioning = false;
        this.transitionProgress = 0;
        this.currentEmotion = null;
        this.expressionResetTimer = null;

        // Micro-expressions
        this.microExpressionTimer = 0;
        this.nextMicroExpression = 4 + Math.random() * 6;
        this.microExpressionTargets = {};
        this.microExpressionCurrent = {};

        this._init();
        this._createCozyRoom();
        this._loadModel();
        this._animate();
    }

    _init() {
        this.renderer = new THREE.WebGLRenderer({
            antialias: true,
            alpha: false,
            powerPreference: 'high-performance',
        });
        this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
        this.renderer.setClearColor(0x0e0c15, 1);
        this.renderer.outputColorSpace = THREE.SRGBColorSpace;
        this.renderer.toneMapping = THREE.ACESFilmicToneMapping;
        this.renderer.toneMappingExposure = 1.2;
        this.renderer.shadowMap.enabled = true;
        this.renderer.shadowMap.type = THREE.PCFSoftShadowMap;
        this.container.appendChild(this.renderer.domElement);

        this.scene = new THREE.Scene();
        this.scene.fog = new THREE.FogExp2(0x0e0c15, 0.05);
        this.camera = new THREE.PerspectiveCamera(CAMERA_FOV, 1, 0.01, 100);

        this._resize();
        window.addEventListener('resize', () => this._resize());
    }

    _resize() {
        const w = this.container.clientWidth;
        const h = this.container.clientHeight;
        if (w === 0 || h === 0) return;
        this.renderer.setSize(w, h);
        this.camera.aspect = w / h;
        this.camera.updateProjectionMatrix();
    }

    _createCozyRoom() {
        // Floor
        const floorMat = new THREE.MeshStandardMaterial({
            color: 0x3d281d,
            roughness: 0.4,
            metalness: 0.1
        });
        const floor = new THREE.Mesh(new THREE.PlaneGeometry(30, 30), floorMat);
        floor.rotation.x = -Math.PI / 2;
        floor.receiveShadow = true;
        this.scene.add(floor);

        // Cozy Carpet
        const carpetMat = new THREE.MeshStandardMaterial({
            color: 0x5a4f7c,
            roughness: 0.9,
        });
        const carpet = new THREE.Mesh(new THREE.CylinderGeometry(1.8, 1.8, 0.01, 32), carpetMat);
        carpet.position.set(0, 0.005, 0);
        carpet.receiveShadow = true;
        this.scene.add(carpet);

        // Back Wall
        const wallMat = new THREE.MeshStandardMaterial({ color: 0x1c1724, roughness: 0.8 });
        const backWall = new THREE.Mesh(new THREE.PlaneGeometry(30, 12), wallMat);
        backWall.position.set(0, 6, -4.5);
        backWall.receiveShadow = true;
        this.scene.add(backWall);

        // Left Wall
        const leftWall = new THREE.Mesh(new THREE.PlaneGeometry(15, 12), wallMat);
        leftWall.position.set(-6, 6, 0);
        leftWall.rotation.y = Math.PI / 2;
        this.scene.add(leftWall);

        // Wood Baseboard
        const baseboardMat = new THREE.MeshStandardMaterial({ color: 0x221812, roughness: 0.6 });
        const baseboard = new THREE.Mesh(new THREE.BoxGeometry(30, 0.25, 0.08), baseboardMat);
        baseboard.position.set(0, 0.125, -4.45);
        baseboard.receiveShadow = true;
        this.scene.add(baseboard);

        // Wooden Shelves
        const woodShelfMat = new THREE.MeshStandardMaterial({ color: 0x472f1e, roughness: 0.5 });
        
        const shelf1 = new THREE.Mesh(new THREE.BoxGeometry(3, 0.08, 0.4), woodShelfMat);
        shelf1.position.set(-1.8, 2.1, -4.3);
        shelf1.castShadow = true;
        shelf1.receiveShadow = true;
        this.scene.add(shelf1);

        const shelf2 = new THREE.Mesh(new THREE.BoxGeometry(2.5, 0.08, 0.4), woodShelfMat);
        shelf2.position.set(1.6, 2.5, -4.3);
        shelf2.castShadow = true;
        shelf2.receiveShadow = true;
        this.scene.add(shelf2);

        // Shelf Decor
        const bookMat1 = new THREE.MeshStandardMaterial({ color: 0xb53c3c, roughness: 0.6 });
        const bookMat2 = new THREE.MeshStandardMaterial({ color: 0x3c78b5, roughness: 0.6 });
        const plantPotMat = new THREE.MeshStandardMaterial({ color: 0xdedede, roughness: 0.3 });
        const leafMat = new THREE.MeshStandardMaterial({ color: 0x4a7c59, roughness: 0.7 });

        const book1 = new THREE.Mesh(new THREE.BoxGeometry(0.1, 0.4, 0.3), bookMat1);
        book1.position.set(-2.2, 2.34, -4.3);
        book1.rotation.y = 0.1;
        book1.castShadow = true;
        this.scene.add(book1);

        const book2 = new THREE.Mesh(new THREE.BoxGeometry(0.08, 0.38, 0.3), bookMat2);
        book2.position.set(-2.1, 2.33, -4.3);
        book2.rotation.z = -0.15;
        book2.castShadow = true;
        this.scene.add(book2);

        const pot = new THREE.Mesh(new THREE.CylinderGeometry(0.18, 0.12, 0.25, 12), plantPotMat);
        pot.position.set(1.4, 2.665, -4.3);
        pot.castShadow = true;
        this.scene.add(pot);

        const plantFoliage = new THREE.Mesh(new THREE.DodecahedronGeometry(0.22, 1), leafMat);
        plantFoliage.position.set(1.4, 2.85, -4.3);
        plantFoliage.castShadow = true;
        this.scene.add(plantFoliage);

        // Warm LED Wall Light strip
        const stripLight = new THREE.Mesh(
            new THREE.PlaneGeometry(4, 0.03),
            new THREE.MeshBasicMaterial({ color: 0xffd294, transparent: true, opacity: 0.7 })
        );
        stripLight.position.set(0, 3.2, -4.48);
        this.scene.add(stripLight);

        const wallGlow = new THREE.PointLight(0xffa868, 0.7, 4);
        wallGlow.position.set(0, 3.1, -4.4);
        this.scene.add(wallGlow);

        // Lighting
        this.scene.add(new THREE.AmbientLight(0x2d243a, 0.65));

        const key = new THREE.DirectionalLight(0xffecd2, 1.4);
        key.position.set(3, 4.5, 2.5);
        key.castShadow = true;
        key.shadow.mapSize.set(2048, 2048);
        key.shadow.camera.near = 0.5;
        key.shadow.camera.far = 12;
        key.shadow.camera.left = -3;
        key.shadow.camera.right = 3;
        key.shadow.camera.top = 3;
        key.shadow.camera.bottom = -1;
        key.shadow.bias = -0.0003;
        key.shadow.normalBias = 0.015;
        this.scene.add(key);

        const fill = new THREE.DirectionalLight(0xd9c8f2, 0.65);
        fill.position.set(-2.5, 2.0, 4.0);
        this.scene.add(fill);

        const rim = new THREE.DirectionalLight(0xb096ff, 0.8);
        rim.position.set(-1.5, 2.5, -2.5);
        this.scene.add(rim);

        const floorBounce = new THREE.DirectionalLight(0xff9f55, 0.25);
        floorBounce.position.set(0, -1, 1);
        this.scene.add(floorBounce);
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
            vrm.scene.traverse((obj) => { obj.frustumCulled = false; });

            this.vrmGroup = new THREE.Group();
            this.vrmGroup.add(vrm.scene);
            this.scene.add(this.vrmGroup);

            vrm.springBoneManager?.reset();
            this.vrmGroup.updateMatrixWorld(true);
            this.vrm = vrm;

            // Ensure LookAt target exists and is added to the scene graph
            if (!this.vrm.lookAt.target) {
                this.vrm.lookAt.target = new THREE.Object3D();
            }
            this.scene.add(this.vrm.lookAt.target);

            try {
                const vrmaAnims = vrmaGltf?.userData?.vrmAnimations;
                if (vrmaAnims && vrmaAnims.length > 0) {
                    const clip = createVRMAnimationClip(vrmaAnims[0], vrm);

                    // Strip ALL arm/hand/shoulder/finger tracks from the VRMA clip
                    // so the mixer never touches those bones — we control them in _updateIdle
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
                    console.log(`[VRM] VRMA: kept ${clip.tracks.length} tracks (stripped arm/hand bones)`);

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

        // Tight close-up framing (0.32 multiplier instead of 0.52)
        const rad = (CAMERA_FOV / 2 * Math.PI) / 180;
        const zDist = (size.y * 0.32) / Math.tan(rad);
        
        // Frame centered tightly on the face/eyes height
        const lookY = center.y + size.y * 0.19;

        this.camera.position.set(
            center.x,
            lookY,
            center.z + zDist
        );
        this.camera.lookAt(center.x, lookY, center.z);

        this.eyeHeight = lookY;
        this.defaultLookAt.set(this.camera.position.x, this.eyeHeight, this.camera.position.z);
        this.fixationTarget.copy(this.defaultLookAt);
    }

    // ── Lip Sync ──
    setAnalyser(a) { this.analyser = a; this.freqData = new Uint8Array(a.frequencyBinCount); }

    _updateLipSync(delta) {
        if (!this.vrm?.expressionManager) return;
        
        let v = { A: 0, E: 0, I: 0, O: 0, U: 0 };
        let hasActiveSpeech = false;
        let avgAmp = 0;
        const AUDIO_THRESHOLD = 0.08; 

        if (this.analyser && this.speaking) {
            this.analyser.getByteFrequencyData(this.freqData);
            const ny = this.analyser.context.sampleRate / 2;
            const bin = ny / this.freqData.length;
            
            const band = (lo, hi) => {
                const a = Math.floor(lo / bin), b = Math.ceil(hi / bin);
                let s = 0, c = 0;
                for (let i = a; i <= b && i < this.freqData.length; i++) { s += this.freqData[i]; c++; }
                return c > 0 ? (s / c) / 255.0 : 0;
            };

            // Capture raw vowel formants
            v.A = band(450, 950) * 1.3; 
            v.E = band(1700, 2600) * 1.1; 
            v.I = band(2400, 3400) * 1.0;
            v.O = band(350, 650) * 1.2; 
            v.U = band(250, 550) * 1.1;

            let sum = 0;
            for (let i = 0; i < this.freqData.length; i++) {
                sum += this.freqData[i];
            }
            avgAmp = (sum / this.freqData.length) / 255.0;
            if (avgAmp > AUDIO_THRESHOLD) {
                hasActiveSpeech = true;
                this.lastAudioTime = performance.now();
            }
        }

        const isMouthIdle = !this.speaking || !hasActiveSpeech || (performance.now() - this.lastAudioTime) > IDLE_MOUTH_TIMEOUT;

        // Smooth vowel transitions to prevent flickering
        const lerpSpeed = 1 - Math.exp(-15 * delta); // smooth filter

        for (const [k, bs] of Object.entries(VOWEL_MAP)) {
            // Drive primary mouth opening (aa) from general volume, and vowels from band intensities
            let targetVal = 0;
            if (!isMouthIdle) {
                if (k === 'A') {
                    // aa is driven by general voice volume for natural jaw movement
                    targetVal = Math.min(avgAmp * 2.5, 0.85);
                } else {
                    targetVal = Math.min(v[k] * 0.8, 0.6);
                }
            }

            const rate = targetVal > this.smoothedVowels[bs] ? LIP_ATTACK : LIP_RELEASE;
            const r = 1 - Math.exp(-rate * delta);
            this.smoothedVowels[bs] += (targetVal - this.smoothedVowels[bs]) * r;

            // Secondary smoothing filter to remove jitters
            this.smoothedVowels[bs] += (targetVal - this.smoothedVowels[bs]) * lerpSpeed;

            if (this.smoothedVowels[bs] < 0.005) {
                this.smoothedVowels[bs] = 0;
            }
            this.vrm.expressionManager.setValue(bs, this.smoothedVowels[bs]);
        }
    }

    // ── Blink ──
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

                if (this.pendingDoubleBlink) {
                    this.pendingDoubleBlink = false;
                    this.nextBlinkTime = MIN_BLINK_INTERVAL + Math.random() * (MAX_BLINK_INTERVAL - MIN_BLINK_INTERVAL);
                } else if (Math.random() < DOUBLE_BLINK_CHANCE) {
                    this.pendingDoubleBlink = true;
                    this.nextBlinkTime = 0.12;
                } else {
                    this.nextBlinkTime = MIN_BLINK_INTERVAL + Math.random() * (MAX_BLINK_INTERVAL - MIN_BLINK_INTERVAL);
                }
            }
        }
    }

    // ── Gaze System (Locked completely to Camera) ──
    _updateSaccades(delta) {
        if (!this.vrm?.lookAt) return;
        
        // Locked completely onto default camera lookAt target. No look-away saccades.
        this.fixationTarget.copy(this.defaultLookAt);

        if (this.vrm.lookAt.target) {
            const lerpSpeed = 1 - Math.exp(-12 * delta); // fast reactive tracking
            this.vrm.lookAt.target.position.lerp(this.fixationTarget, lerpSpeed);
            this.vrm.lookAt.update(delta);
        }
    }

    // ── Expressions ──
    _ease(t) { return t < 0.5 ? 4*t*t*t : 1 - Math.pow(-2*t+2,3)/2; }

    _setEmotion(name, intensity = 1) {
        if (!this.vrm?.expressionManager) return;
        clearTimeout(this.expressionResetTimer);
        const st = EMOTION_MAP[name];
        if (!st) return;
        this.currentEmotion = name;
        this.isTransitioning = true;
        this.transitionProgress = 0;
        this.currentExpressionValues.clear();
        this.targetExpressionValues.clear();
        const map = this.vrm.expressionManager.expressionMap || this.vrm.expressionManager._expressionMap;
        if (map) {
            for (const n of Object.keys(map)) {
                this.currentExpressionValues.set(n, this.vrm.expressionManager.getValue(n) || 0);
                this.targetExpressionValues.set(n, 0);
            }
        }
        const norm = Math.min(1, Math.max(0, intensity));
        for (const e of st.expression || []) {
            this.targetExpressionValues.set(e.name, e.value * norm);
        }
    }

    _updateExpressions(delta) {
        if (!this.isTransitioning || !this.currentEmotion || !this.vrm?.expressionManager) return;
        const dur = EMOTION_MAP[this.currentEmotion]?.blendDuration || 0.3;
        this.transitionProgress += delta / dur;
        if (this.transitionProgress >= 1) { this.transitionProgress = 1; this.isTransitioning = false; }
        const e = this._ease(this.transitionProgress);
        for (const [n, t] of this.targetExpressionValues) {
            const s = this.currentExpressionValues.get(n) || 0;
            this.vrm.expressionManager.setValue(n, s + (t - s) * e);
        }
    }

    _updateMicroExpressions(delta) {
        if (!this.vrm?.expressionManager) return;
        if (this.speaking || this.isTransitioning) return;

        this.microExpressionTimer += delta;
        if (this.microExpressionTimer >= this.nextMicroExpression) {
            this.microExpressionTimer = 0;
            this.nextMicroExpression = 5 + Math.random() * 7;

            const options = [
                { name: 'happy', value: Math.random() * 0.10 },
                { name: 'neutral', value: 0.85 + Math.random() * 0.15 },
            ];
            const chosen = options[Math.floor(Math.random() * options.length)];
            this.microExpressionTargets = { [chosen.name]: chosen.value };
        }

        for (const [name, target] of Object.entries(this.microExpressionTargets)) {
            const current = this.microExpressionCurrent[name] || 0;
            const speed = 1 - Math.exp(-0.7 * delta);
            this.microExpressionCurrent[name] = current + (target - current) * speed;
            if (!this.isTransitioning) {
                const existing = this.vrm.expressionManager.getValue(name) || 0;
                if (existing < 0.15) {
                    this.vrm.expressionManager.setValue(name, this.microExpressionCurrent[name]);
                }
            }
        }
    }

    // ── Public ──
    setEmotion(emotion, intensity) {
        this._setEmotion(emotion || 'calm', intensity || 0.5);
        clearTimeout(this.expressionResetTimer);
        this.expressionResetTimer = setTimeout(() => this._setEmotion('neutral', 1), EXPRESSION_RESET_MS);
    }
    setSpeaking(s) {
        this.speaking = s;
        if (!s) {
            for (const bs of Object.values(VOWEL_MAP)) {
                this.smoothedVowels[bs] = 0;
                if (this.vrm?.expressionManager) {
                    this.vrm.expressionManager.setValue(bs, 0);
                }
            }
        }
    }

    // ── Organic Idle Animation (No Cumulative Drift / Absolute Assignments) ──
    _updateIdle(elapsed, delta) {
        if (!this.vrm?.humanoid) return;
        const h = this.vrm.humanoid;

        // Set absolute bone rotations relative to idle defaults rather than compounding with +=
        const setIdleRotation = (boneName, x, y, z) => {
            const node = h.getNormalizedBoneNode(boneName);
            if (!node) return;
            node.rotation.x = x;
            node.rotation.y = y;
            node.rotation.z = z;
        };

        // 1. Hips (Gentle weight shift sway)
        const sway = Math.sin(elapsed * 0.12) * Math.sin(elapsed * 0.20);
        const hipsNode = h.getNormalizedBoneNode('hips');
        if (hipsNode) {
            if (this.hipsBaseX === null) this.hipsBaseX = hipsNode.position.x;
            if (this.hipsBaseY === null) this.hipsBaseY = hipsNode.position.y;
            if (this.hipsBaseZ === null) this.hipsBaseZ = hipsNode.position.z;
            
            hipsNode.position.x = this.hipsBaseX + sway * 0.005;
            hipsNode.rotation.z = sway * 0.003;
        }

        // 2. Breathing (Spine & Chest)
        const breath = Math.sin(elapsed * 0.80);
        const breath2 = Math.sin(elapsed * 0.80 + 0.35);
        setIdleRotation('spine', breath * 0.006 + breath2 * 0.001, 0, Math.sin(elapsed * 0.12) * 0.0008);
        setIdleRotation('chest', breath * 0.004, 0, 0);

        // 3. Head & Neck (Subtle natural human sway)
        const t = elapsed;
        const headPitch = Math.sin(t * 0.25 + 0.6) * 0.003 + Math.sin(t * 0.5) * 0.001;
        const headYaw   = Math.sin(t * 0.18) * 0.005 + Math.sin(t * 0.35) * 0.002;
        const headRoll  = Math.sin(t * 0.15 + 1.1) * 0.002;

        setIdleRotation('head', clamp(headPitch, -0.14, 0.14), clamp(headYaw, -0.21, 0.21), clamp(headRoll, -0.09, 0.09));
        setIdleRotation('neck', clamp(headPitch * 0.4, -0.08, 0.08), clamp(headYaw * 0.4, -0.12, 0.12), clamp(headRoll * 0.3, -0.05, 0.05));

        // 4. Natural arm resting pose (Relaxed, inward shoulders, slightly bent elbows)
        // Corrected Z rotation to bring T-pose arms all the way down (-1.25 left, +1.25 right)
        const leftArmZ = -1.25 + Math.sin(elapsed * 0.15) * 0.005;
        const rightArmZ = 1.25 - Math.sin(elapsed * 0.15) * 0.005;

        setIdleRotation('leftUpperArm', 0.05, 0.05, leftArmZ);
        setIdleRotation('rightUpperArm', 0.05, -0.05, rightArmZ);

        setIdleRotation('leftLowerArm', -0.35 + Math.sin(elapsed * 0.2) * 0.005, 0.1, 0);
        setIdleRotation('rightLowerArm', -0.35 - Math.sin(elapsed * 0.2) * 0.005, -0.1, 0);
    }

    _animate() {
        requestAnimationFrame(() => this._animate());
        const delta = Math.min(this.clock.getDelta(), 0.1);
        const elapsed = this.clock.getElapsedTime();

        if (this.vrm) {
            // 1. Mixer applies VRMA keyframes to normalized bones (arms stripped)
            if (this.mixer) this.mixer.update(delta);

            // 2. Our idle overrides on normalized bones (arms, breathing, head)
            this._updateIdle(elapsed, delta);

            // 3. Gaze / LookAt
            this._updateSaccades(delta);

            // 4. NOW commit normalized → raw bones (includes our arm overrides)
            this.vrm.humanoid?.update();

            // 5. Spring bone physics (uses final raw bone positions)
            this.vrm.springBoneManager?.update(delta);

            // 6. Expressions (blink, emotions, lip sync)
            this._updateBlink(delta);
            this._updateExpressions(delta);
            this._updateMicroExpressions(delta);
            this._updateLipSync(delta);

            this.vrm.expressionManager?.update();
            this.vrm.nodeConstraintManager?.update();
        }

        this.renderer.render(this.scene, this.camera);
    }
}

window.CharacterRenderer = CharacterRenderer;
