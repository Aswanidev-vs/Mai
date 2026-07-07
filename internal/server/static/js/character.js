import * as THREE from 'three';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';
import { VRMLoaderPlugin, VRMUtils } from '@pixiv/three-vrm';
import { VRMAnimationLoaderPlugin, createVRMAnimationClip } from '@pixiv/three-vrm-animation';

const CAMERA_FOV = 40;
const BLINK_DURATION = 0.2;
const MIN_BLINK_INTERVAL = 1;
const MAX_BLINK_INTERVAL = 6;
const LIP_ATTACK = 50;
const LIP_RELEASE = 30;
const LIP_CAP = 0.7;
const IDLE_MOUTH_TIMEOUT = 160;
const EXPRESSION_RESET_MS = 3000;

const EMOTION_MAP = {
    happy:     { expression: [{ name: 'happy', value: 0.7 }, { name: 'aa', value: 0.2 }], blendDuration: 0.4 },
    sad:       { expression: [{ name: 'sad', value: 0.7 }, { name: 'oh', value: 0.15 }], blendDuration: 0.4 },
    angry:     { expression: [{ name: 'angry', value: 0.7 }, { name: 'ee', value: 0.3 }], blendDuration: 0.3 },
    surprised: { expression: [{ name: 'surprised', value: 0.8 }, { name: 'oh', value: 0.4 }], blendDuration: 0.15 },
    neutral:   { expression: [{ name: 'neutral', value: 1.0 }], blendDuration: 0.6 },
    think:     { expression: [{ name: 'think', value: 0.7 }], blendDuration: 0.5 },
    calm:      { expression: [{ name: 'neutral', value: 1.0 }], blendDuration: 0.6 },
    stressed:  { expression: [{ name: 'angry', value: 0.7 }, { name: 'ee', value: 0.3 }], blendDuration: 0.3 },
    excited:   { expression: [{ name: 'surprised', value: 0.8 }, { name: 'oh', value: 0.4 }], blendDuration: 0.15 },
    frustrated:{ expression: [{ name: 'angry', value: 0.7 }, { name: 'ee', value: 0.3 }], blendDuration: 0.3 },
};

const VOWEL_MAP = { A: 'aa', E: 'ee', I: 'ih', O: 'oh', U: 'ou' };

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

        // Saccades
        this.fixationTarget = new THREE.Vector3(0, 1.3, 0);
        this.timeSinceLastSaccade = 0;
        this.nextSaccadeAfter = 2;
        this.defaultLookAt = new THREE.Vector3(0, 1.3, 0);
        this.eyeHeight = 1.3;

        // Expression
        this.currentExpressionValues = new Map();
        this.targetExpressionValues = new Map();
        this.isTransitioning = false;
        this.transitionProgress = 0;
        this.currentEmotion = null;
        this.expressionResetTimer = null;

        this._init();
        this._createRoom();
        this._loadModel();
        this._animate();
    }

    _init() {
        this.renderer = new THREE.WebGLRenderer({ antialias: true });
        this.renderer.setPixelRatio(window.devicePixelRatio);
        this.renderer.setClearColor(0x0a0a14, 1);
        this.renderer.outputColorSpace = THREE.SRGBColorSpace;
        this.renderer.toneMapping = THREE.ACESFilmicToneMapping;
        this.renderer.toneMappingExposure = 1.0;
        this.renderer.shadowMap.enabled = true;
        this.renderer.shadowMap.type = THREE.PCFSoftShadowMap;
        this.container.appendChild(this.renderer.domElement);

        this.scene = new THREE.Scene();
        this.scene.fog = new THREE.FogExp2(0x0a0a14, 0.06);
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

    _createRoom() {
        // Floor
        const floor = new THREE.Mesh(
            new THREE.PlaneGeometry(30, 30),
            new THREE.MeshStandardMaterial({ color: 0x111122, roughness: 0.85, metalness: 0.15 })
        );
        floor.rotation.x = -Math.PI / 2;
        floor.receiveShadow = true;
        this.scene.add(floor);

        // Back wall
        const wallMat = new THREE.MeshStandardMaterial({ color: 0x0d0d1a, roughness: 0.9 });
        const back = new THREE.Mesh(new THREE.PlaneGeometry(30, 10), wallMat);
        back.position.set(0, 5, -6);
        this.scene.add(back);

        // Side walls
        const left = new THREE.Mesh(new THREE.PlaneGeometry(12, 10), wallMat);
        left.position.set(-6, 5, 0);
        left.rotation.y = Math.PI / 2;
        this.scene.add(left);

        const right = new THREE.Mesh(new THREE.PlaneGeometry(12, 10), wallMat);
        right.position.set(6, 5, 0);
        right.rotation.y = -Math.PI / 2;
        this.scene.add(right);

        // Accent strip
        const strip = new THREE.Mesh(
            new THREE.PlaneGeometry(5, 0.04),
            new THREE.MeshBasicMaterial({ color: 0x7c6aef, transparent: true, opacity: 0.5 })
        );
        strip.position.set(0, 2.2, -5.98);
        this.scene.add(strip);

        // Lighting
        this.scene.add(new THREE.AmbientLight(0x222244, 0.4));

        const key = new THREE.DirectionalLight(0xfff0e6, 0.9);
        key.position.set(2, 4, 3);
        key.castShadow = true;
        key.shadow.mapSize.set(1024, 1024);
        key.shadow.camera.near = 0.5;
        key.shadow.camera.far = 15;
        key.shadow.camera.left = -4;
        key.shadow.camera.right = 4;
        key.shadow.camera.top = 4;
        key.shadow.camera.bottom = -1;
        this.scene.add(key);

        this.scene.add(new THREE.DirectionalLight(0x7c6aef, 0.3).translateX(-2).translateY(2).translateZ(1));
        this.scene.add(new THREE.DirectionalLight(0x7c6aef, 0.4).translateY(3).translateZ(-3));
        this.scene.add(new THREE.PointLight(0x7c6aef, 0.25, 5).translateY(1.5).translateZ(1));
    }

    _loadModel() {
        const loader = new GLTFLoader();
        loader.register((parser) => new VRMLoaderPlugin(parser));
        loader.register((parser) => new VRMAnimationLoaderPlugin(parser));

        // Load VRM and VRMA in parallel
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

            // Rotate to face camera
            if (vrm.lookAt) {
                const facing = vrm.lookAt.faceFront.clone().normalize();
                const target = new THREE.Vector3(0, 0, -1);
                const q = new THREE.Quaternion();
                q.setFromUnitVectors(facing, target);
                this.vrmGroup.quaternion.premultiply(q);
                this.vrmGroup.updateMatrixWorld(true);
            }

            vrm.springBoneManager?.reset();
            this.vrmGroup.updateMatrixWorld(true);
            this.vrm = vrm;

            // Play VRMA idle animation — exits T-pose
            try {
                const vrmaAnims = vrmaGltf?.userData?.vrmAnimations;
                if (vrmaAnims && vrmaAnims.length > 0) {
                    const clip = createVRMAnimationClip(vrmaAnims[0], vrm);
                    this.mixer = new THREE.AnimationMixer(vrm.scene);
                    const action = this.mixer.clipAction(clip);
                    action.play();
                    console.log('[VRM] VRMA idle playing:', clip.name, 'duration:', clip.duration.toFixed(2) + 's');
                } else {
                    console.warn('[VRM] No vrmAnimations in VRMA file. userData:', Object.keys(vrmaGltf?.userData || {}));
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

        // Full body framing with padding
        const rad = (CAMERA_FOV / 2 * Math.PI) / 180;
        const zDist = (size.y * 1.15) / Math.tan(rad);

        this.camera.position.set(
            center.x + size.x * 0.05,
            center.y + size.y * 0.05,
            center.z + zDist
        );
        this.camera.lookAt(center.x, center.y + size.y * 0.05, center.z);

        // Eye height
        const head = this.vrm.humanoid?.getNormalizedBoneNode('head');
        if (head) {
            const p = new THREE.Vector3();
            head.getWorldPosition(p);
            this.eyeHeight = p.y;
        } else {
            this.eyeHeight = center.y + size.y * 0.35;
        }
        this.defaultLookAt.set(center.x, this.eyeHeight, center.z);
        this.fixationTarget.copy(this.defaultLookAt);
    }

    // ── Lip Sync ──
    setAnalyser(a) { this.analyser = a; this.freqData = new Uint8Array(a.frequencyBinCount); }

    _updateLipSync(delta) {
        if (!this.vrm?.expressionManager) return;
        let v = { A: 0, E: 0, I: 0, O: 0, U: 0 };
        if (this.analyser && this.speaking) {
            this.analyser.getByteFrequencyData(this.freqData);
            const ny = this.analyser.context.sampleRate / 2;
            const bin = ny / this.freqData.length;
            const band = (lo, hi) => {
                const a = Math.floor(lo / bin), b = Math.ceil(hi / bin);
                let s = 0, c = 0;
                for (let i = a; i <= b && i < this.freqData.length; i++) { s += this.freqData[i]; c++; }
                return c > 0 ? s / c / 255 : 0;
            };
            v.A = band(500, 1000); v.E = band(1800, 2800); v.I = band(2500, 3500);
            v.O = band(400, 700); v.U = band(300, 600);
            this.lastAudioTime = performance.now();
        }
        const idle = (performance.now() - this.lastAudioTime) > IDLE_MOUTH_TIMEOUT;
        for (const [k, bs] of Object.entries(VOWEL_MAP)) {
            const t = idle ? 0 : Math.min(v[k] * 0.9, 1) ** 0.7 * LIP_CAP;
            const r = 1 - Math.exp(-(t > this.smoothedVowels[bs] ? LIP_ATTACK : LIP_RELEASE) * delta);
            this.smoothedVowels[bs] += (t - this.smoothedVowels[bs]) * r;
            if (this.smoothedVowels[bs] < 0.01) this.smoothedVowels[bs] = 0;
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
                this.nextBlinkTime = MIN_BLINK_INTERVAL + Math.random() * (MAX_BLINK_INTERVAL - MIN_BLINK_INTERVAL);
            }
        }
    }

    // ── Saccades ──
    _updateSaccades(delta) {
        if (!this.vrm?.lookAt) return;
        this.timeSinceLastSaccade += delta;
        if (this.timeSinceLastSaccade >= this.nextSaccadeAfter) {
            this.fixationTarget.set(
                this.defaultLookAt.x + (Math.random() - 0.5) * 0.25,
                this.defaultLookAt.y + (Math.random() - 0.5) * 0.15,
                this.defaultLookAt.z
            );
            this.timeSinceLastSaccade = 0;
            this.nextSaccadeAfter = (800 + Math.random() * 3200) / 1000;
        }
        if (!this.vrm.lookAt.target) this.vrm.lookAt.target = new THREE.Object3D();
        this.vrm.lookAt.target.position.lerp(this.fixationTarget, 1);
        this.vrm.lookAt.update(delta);
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

    // ── Public ──
    setEmotion(emotion, intensity) {
        this._setEmotion(emotion || 'calm', intensity || 0.5);
        clearTimeout(this.expressionResetTimer);
        this.expressionResetTimer = setTimeout(() => this._setEmotion('neutral', 1), EXPRESSION_RESET_MS);
    }
    setSpeaking(s) { this.speaking = s; }

    // ── Render Loop ──
    _animate() {
        requestAnimationFrame(() => this._animate());
        const delta = Math.min(this.clock.getDelta(), 0.1);
        const elapsed = this.clock.getElapsedTime();

        if (this.vrm) {
            // Animation mixer first (applies VRMA idle pose)
            if (this.mixer) this.mixer.update(delta);

            // VRM systems
            this.vrm.springBoneManager?.update(delta);
            this.vrm.humanoid?.update();
            this.vrm.lookAt?.update(delta);

            // Our procedural layers
            this._updateBlink(delta);
            this._updateSaccades(delta);
            this._updateExpressions(delta);
            this._updateLipSync(delta);

            this.vrm.expressionManager?.update();
            this.vrm.nodeConstraintManager?.update();
        }

        this.renderer.render(this.scene, this.camera);
    }
}

window.CharacterRenderer = CharacterRenderer;
