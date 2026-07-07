import * as THREE from 'three';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';
import { VRMLoaderPlugin, VRMUtils } from '@pixiv/three-vrm';
import { VRMAnimationLoaderPlugin, createVRMAnimationClip } from '@pixiv/three-vrm-animation';

const CAMERA_FOV = 30;
const BLINK_DURATION = 0.15;
const MIN_BLINK_INTERVAL = 1.5;
const MAX_BLINK_INTERVAL = 5;
const DOUBLE_BLINK_CHANCE = 0.22;
const LIP_ATTACK = 55;
const LIP_RELEASE = 28;
const EXPRESSION_RESET_MS = 4000;

// "Alive" tuning
const REST_IDLE_SECONDS = 28;       // time before she slips into a calmer "rest" state
const GAZE_SHIFT_MIN = 2.5;         // seconds between micro-saccades
const GAZE_SHIFT_MAX = 6.0;
const WANDER_CHANCE = 0.25;         // chance a gaze shift is a longer "look around the room"
const IDLE_BEHAVIOR_MIN = 18;       // seconds between occasional idle behaviors
const IDLE_BEHAVIOR_MAX = 38;

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

        // Micro-expressions
        this.microExpressionTimer = 0;
        this.nextMicroExpression = 4 + secureRand() * 6;
        this.microExpressionTargets = {};
        this.microExpressionCurrent = {};

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

        this._init();
        this._createCozyRoom();
        this._loadModel();
        this._animate();
    }

    _rand(a, b) { return a + secureRand() * (b - a); }

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
        this.renderer.toneMappingExposure = 1.15;
        this.renderer.shadowMap.enabled = true;
        this.renderer.shadowMap.type = THREE.PCFSoftShadowMap;
        this.container.appendChild(this.renderer.domElement);

        this.scene = new THREE.Scene();
        this.scene.fog = new THREE.FogExp2(0x140f1d, 0.045);
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

    // ── Cozy, lived-in room: study + bedroom nook ──
    _createCozyRoom() {
        const add = (mesh, cast = true, receive = false) => {
            mesh.castShadow = cast;
            mesh.receiveShadow = receive;
            this.scene.add(mesh);
            return mesh;
        };

        // ── Floor: warm wood planks ──
        const floorMat = new THREE.MeshStandardMaterial({ color: 0x4a3322, roughness: 0.55, metalness: 0.05 });
        const floor = new THREE.Mesh(new THREE.PlaneGeometry(40, 40), floorMat);
        floor.rotation.x = -Math.PI / 2;
        floor.receiveShadow = true;
        floor.castShadow = false;
        this.scene.add(floor);

        // Subtle plank lines
        const plankMat = new THREE.MeshStandardMaterial({ color: 0x3c2a1c, roughness: 0.7 });
        for (let i = -6; i <= 6; i++) {
            const plank = new THREE.Mesh(new THREE.BoxGeometry(0.02, 0.01, 40), plankMat);
            plank.position.set(i * 1.4, 0.006, 0);
            plank.receiveShadow = true;
            plank.castShadow = false;
            this.scene.add(plank);
        }

        // ── Big soft rug under her ──
        const rugMat = new THREE.MeshStandardMaterial({ color: 0x6b5b95, roughness: 0.95 });
        const rug = new THREE.Mesh(new THREE.CylinderGeometry(2.4, 2.4, 0.02, 48), rugMat);
        rug.position.set(0, 0.011, 0.2);
        rug.receiveShadow = true;
        rug.castShadow = false;
        this.scene.add(rug);
        const rugInner = new THREE.Mesh(new THREE.CylinderGeometry(1.7, 1.7, 0.022, 48),
            new THREE.MeshStandardMaterial({ color: 0x8a7bb5, roughness: 0.95 }));
        rugInner.position.set(0, 0.012, 0.2);
        rugInner.receiveShadow = true;
        rugInner.castShadow = false;
        this.scene.add(rugInner);

        // ── Walls ──
        const wallMat = new THREE.MeshStandardMaterial({ color: 0x241d30, roughness: 0.9 });
        const backWall = new THREE.Mesh(new THREE.PlaneGeometry(40, 14), wallMat);
        backWall.position.set(0, 7, -5.2);
        backWall.receiveShadow = true;
        backWall.castShadow = false;
        this.scene.add(backWall);

        const leftWall = new THREE.Mesh(new THREE.PlaneGeometry(18, 14), wallMat);
        leftWall.position.set(-7, 7, 0);
        leftWall.rotation.y = Math.PI / 2;
        leftWall.receiveShadow = true;
        leftWall.castShadow = false;
        this.scene.add(leftWall);

        // Baseboard
        const baseboardMat = new THREE.MeshStandardMaterial({ color: 0x2a1d12, roughness: 0.6 });
        const baseboard = new THREE.Mesh(new THREE.BoxGeometry(40, 0.3, 0.1), baseboardMat);
        baseboard.position.set(0, 0.15, -5.14);
        baseboard.receiveShadow = true;
        baseboard.castShadow = false;
        this.scene.add(baseboard);

        // ── Bed / nook (back-right corner) ──
        const bedFrameMat = new THREE.MeshStandardMaterial({ color: 0x5b3d2b, roughness: 0.6 });
        const bedMat = new THREE.MeshStandardMaterial({ color: 0x9a6f8e, roughness: 0.9 });
        const blanketMat = new THREE.MeshStandardMaterial({ color: 0xc9b6d6, roughness: 0.95 });
        const pillowMat = new THREE.MeshStandardMaterial({ color: 0xf2e9f2, roughness: 0.95 });

        const bedBase = new THREE.Mesh(new THREE.BoxGeometry(3.2, 0.5, 2.0), bedFrameMat);
        bedBase.position.set(3.4, 0.25, -4.0);
        add(bedBase, true, true);
        const mattress = new THREE.Mesh(new THREE.BoxGeometry(3.0, 0.35, 1.85), bedMat);
        mattress.position.set(3.4, 0.62, -4.0);
        add(mattress, true, true);
        const blanket = new THREE.Mesh(new THREE.BoxGeometry(3.05, 0.18, 1.1), blanketMat);
        blanket.position.set(3.4, 0.82, -4.35);
        add(blanket, true, true);
        const pillow = new THREE.Mesh(new THREE.BoxGeometry(1.0, 0.22, 0.6), pillowMat);
        pillow.position.set(3.4, 0.85, -3.35);
        pillow.rotation.x = -0.1;
        add(pillow, true, true);

        // ── Bookshelf (left wall) with many books ──
        const woodMat = new THREE.MeshStandardMaterial({ color: 0x4a3320, roughness: 0.5 });
        const shelfBoard = new THREE.Mesh(new THREE.BoxGeometry(0.4, 0.08, 3.2), woodMat);
        for (let s = 0; s < 4; s++) {
            const board = shelfBoard.clone();
            board.position.set(-6.7, 1.0 + s * 1.1, -1.0);
            add(board, true, true);
        }
        const sideL = new THREE.Mesh(new THREE.BoxGeometry(0.4, 4.4, 0.08), woodMat);
        sideL.position.set(-6.7, 2.2, -2.55); add(sideL, true, true);
        const sideR = sideL.clone(); sideR.position.set(-6.7, 2.2, 0.55); add(sideR, true, true);
        const backPanel = new THREE.Mesh(new THREE.BoxGeometry(0.05, 4.4, 3.2), woodMat);
        backPanel.position.set(-6.5, 2.2, -1.0); add(backPanel, true, true);

        const bookColors = [0xb53c3c, 0x3c78b5, 0x4a9d5b, 0xd9a441, 0x8e5bb5, 0xc96f8e, 0x4a8c9d, 0xd06b3a];
        for (let s = 0; s < 4; s++) {
            let x = -6.45;
            const count = 7 + Math.floor(secureRand() * 3);
            for (let b = 0; b < count; b++) {
                const h = 0.55 + secureRand() * 0.35;
                const w = 0.09 + secureRand() * 0.05;
                const lean = (b === count - 1) ? secureRand() * 0.25 : 0;
                const book = new THREE.Mesh(
                    new THREE.BoxGeometry(w, h, 0.55),
                    new THREE.MeshStandardMaterial({ color: bookColors[(s * 3 + b) % bookColors.length], roughness: 0.6 })
                );
                book.position.set(-6.5 + lean * 0.2, 1.0 + s * 1.1 + h / 2 + 0.04, x);
                book.rotation.z = lean;
                add(book, true, false);
                x += w + 0.02;
            }
        }

        // ── Desk (right of center, front) ──
        const deskMat = new THREE.MeshStandardMaterial({ color: 0x6b4a2f, roughness: 0.5 });
        const deskTop = new THREE.Mesh(new THREE.BoxGeometry(2.4, 0.12, 1.0), deskMat);
        deskTop.position.set(-2.6, 1.0, -3.6); add(deskTop, true, true);
        for (const dx of [-1.1, 1.1]) {
            const leg = new THREE.Mesh(new THREE.BoxGeometry(0.12, 1.0, 0.12), deskMat);
            leg.position.set(-2.6 + dx, 0.5, -3.6); add(leg, true, true);
        }
        // Desk lamp (emissive shade + warm glow)
        const lampArm = new THREE.Mesh(new THREE.CylinderGeometry(0.03, 0.03, 0.7, 8),
            new THREE.MeshStandardMaterial({ color: 0x2c2c33, roughness: 0.4, metalness: 0.6 }));
        lampArm.position.set(-3.4, 1.45, -3.6); lampArm.rotation.z = 0.5; add(lampArm, true, false);
        const lampShade = new THREE.Mesh(new THREE.ConeGeometry(0.18, 0.22, 16, 1, true),
            new THREE.MeshStandardMaterial({ color: 0xffd9a0, emissive: 0xffcaa0, emissiveIntensity: 1.4, roughness: 0.5, side: THREE.DoubleSide }));
        lampShade.position.set(-3.2, 1.7, -3.6); add(lampShade, false, false);
        const lampLight = new THREE.PointLight(0xffcf9e, 1.1, 5);
        lampLight.position.set(-3.2, 1.6, -3.4); this.scene.add(lampLight);
        // Mug
        const mug = new THREE.Mesh(new THREE.CylinderGeometry(0.1, 0.08, 0.18, 12),
            new THREE.MeshStandardMaterial({ color: 0xd9663f, roughness: 0.5 }));
        mug.position.set(-2.2, 1.12, -3.5); add(mug, true, false);
        // Little potted plant on desk
        const deskPot = new THREE.Mesh(new THREE.CylinderGeometry(0.1, 0.08, 0.14, 10),
            new THREE.MeshStandardMaterial({ color: 0xcfcfcf, roughness: 0.4 }));
        deskPot.position.set(-1.9, 1.13, -3.7); add(deskPot, true, false);
        const deskPlant = new THREE.Mesh(new THREE.IcosahedronGeometry(0.16, 1),
            new THREE.MeshStandardMaterial({ color: 0x4a8c5b, roughness: 0.7 }));
        deskPlant.position.set(-1.9, 1.3, -3.7); add(deskPlant, true, false);
        // Laptop (box + faintly emissive screen)
        const laptopBase = new THREE.Mesh(new THREE.BoxGeometry(0.5, 0.03, 0.36),
            new THREE.MeshStandardMaterial({ color: 0x9a9aa5, roughness: 0.3, metalness: 0.5 }));
        laptopBase.position.set(-2.4, 1.07, -3.5); add(laptopBase, true, false);
        const laptopScreen = new THREE.Mesh(new THREE.BoxGeometry(0.5, 0.32, 0.03),
            new THREE.MeshStandardMaterial({ color: 0x10202a, emissive: 0x2a6fb0, emissiveIntensity: 0.5, roughness: 0.2 }));
        laptopScreen.position.set(-2.4, 1.24, -3.62); laptopScreen.rotation.x = -0.25; add(laptopScreen, false, false);

        // ── Wall art / framed photos ──
        const frameMat = new THREE.MeshStandardMaterial({ color: 0x2a1d12, roughness: 0.6 });
        const artColors = [0x6b8fd9, 0xd98fb0, 0x8fd9a8];
        for (let i = 0; i < 3; i++) {
            const frame = new THREE.Mesh(new THREE.BoxGeometry(0.7, 0.9, 0.05), frameMat);
            frame.position.set(-4.5 + i * 1.6, 3.4, -5.1); add(frame, true, false);
            const art = new THREE.Mesh(new THREE.PlaneGeometry(0.55, 0.75),
                new THREE.MeshStandardMaterial({ color: artColors[i], roughness: 0.8 }));
            art.position.set(-4.5 + i * 1.6, 3.4, -5.07); add(art, false, false);
        }

        // ── String / fairy lights across the top of the back wall ──
        const fairyMat = new THREE.MeshStandardMaterial({ color: 0xffe6b0, emissive: 0xffd98a, emissiveIntensity: 2.2 });
        const wireMat = new THREE.MeshStandardMaterial({ color: 0x1a1a22, roughness: 0.8 });
        const wire = new THREE.Mesh(new THREE.CylinderGeometry(0.01, 0.01, 11, 6), wireMat);
        wire.rotation.z = Math.PI / 2; wire.position.set(0, 5.2, -5.15); this.scene.add(wire);
        for (let i = 0; i < 22; i++) {
            const x = -5.2 + i * 0.5;
            const y = 5.2 - Math.abs(Math.sin(i * 0.6)) * 0.35;
            const bulb = new THREE.Mesh(new THREE.SphereGeometry(0.05, 8, 8), fairyMat);
            bulb.position.set(x, y, -5.12); this.scene.add(bulb);
        }
        const fairyGlow = new THREE.PointLight(0xffd98a, 0.5, 8);
        fairyGlow.position.set(0, 5.0, -4.6); this.scene.add(fairyGlow);

        // ── Window with soft daylight (back wall, left of center) ──
        const windowFrame = new THREE.Mesh(new THREE.BoxGeometry(1.8, 2.4, 0.1), frameMat);
        windowFrame.position.set(1.6, 3.6, -5.1); add(windowFrame, true, false);
        const windowPane = new THREE.Mesh(new THREE.PlaneGeometry(1.5, 2.1),
            new THREE.MeshStandardMaterial({ color: 0xbfd4e8, emissive: 0x9fc0e8, emissiveIntensity: 0.8, roughness: 0.3 }));
        windowPane.position.set(1.6, 3.6, -5.04); add(windowPane, false, false);
        const dayGlow = new THREE.PointLight(0xbcd4f0, 0.45, 7);
        dayGlow.position.set(1.6, 3.4, -4.5); this.scene.add(dayGlow);

        // ── Plants ──
        const potMat = new THREE.MeshStandardMaterial({ color: 0xdedede, roughness: 0.3 });
        const leafMat = new THREE.MeshStandardMaterial({ color: 0x4a7c59, roughness: 0.7 });
        // Floor plant (right)
        const fpot = new THREE.Mesh(new THREE.CylinderGeometry(0.28, 0.2, 0.4, 14), potMat);
        fpot.position.set(5.0, 0.2, -3.5); add(fpot, true, true);
        for (let i = 0; i < 5; i++) {
            const leaf = new THREE.Mesh(new THREE.DodecahedronGeometry(0.28 + secureRand() * 0.12, 1), leafMat);
            leaf.position.set(5.0 + (secureRand() - 0.5) * 0.3, 0.6 + secureRand() * 0.4, -3.5 + (secureRand() - 0.5) * 0.3);
            add(leaf, true, false);
        }
        // Hanging plant (top-left)
        const hpot = new THREE.Mesh(new THREE.CylinderGeometry(0.18, 0.14, 0.22, 12), potMat);
        hpot.position.set(-5.2, 4.6, -3.0); add(hpot, true, false);
        for (let i = 0; i < 6; i++) {
            const hleaf = new THREE.Mesh(new THREE.ConeGeometry(0.05, 0.7 + secureRand() * 0.3, 6), leafMat);
            hleaf.position.set(-5.2 + (secureRand() - 0.5) * 0.2, 4.2 - secureRand() * 0.3, -3.0 + (secureRand() - 0.5) * 0.2);
            add(hleaf, false, false);
        }
        // Book on the rug (lived-in detail)
        const bookOnRug = new THREE.Mesh(new THREE.BoxGeometry(0.4, 0.06, 0.3),
            new THREE.MeshStandardMaterial({ color: 0x3c78b5, roughness: 0.6 }));
        bookOnRug.position.set(1.3, 0.03, 1.4); bookOnRug.rotation.y = 0.4; add(bookOnRug, true, true);

        // ── Sleeping cat (lived-in detail) ──
        const catMat = new THREE.MeshStandardMaterial({ color: 0x2a2a30, roughness: 0.8 });
        const catBody = new THREE.Mesh(new THREE.CapsuleGeometry(0.16, 0.4, 4, 8), catMat);
        catBody.rotation.z = Math.PI / 2; catBody.position.set(-1.4, 0.16, 1.2); add(catBody, true, false);
        const catHead = new THREE.Mesh(new THREE.SphereGeometry(0.14, 10, 10), catMat);
        catHead.position.set(-1.05, 0.16, 1.2); add(catHead, true, false);
        const catTail = new THREE.Mesh(new THREE.CapsuleGeometry(0.05, 0.35, 4, 6), catMat);
        catTail.position.set(-1.7, 0.18, 1.35); catTail.rotation.z = 0.6; add(catTail, true, false);

        // ── Floating dust motes (atmosphere / life) ──
        const moteCount = 90;
        const motePos = new Float32Array(moteCount * 3);
        for (let i = 0; i < moteCount; i++) {
            motePos[i * 3] = (secureRand() - 0.5) * 12;
            motePos[i * 3 + 1] = secureRand() * 8;
            motePos[i * 3 + 2] = -4 + secureRand() * 5;
        }
        const moteGeo = new THREE.BufferGeometry();
        moteGeo.setAttribute('position', new THREE.BufferAttribute(motePos, 3));
        this.dustMotes = new THREE.Points(moteGeo, new THREE.PointsMaterial({
            color: 0xffe9c4, size: 0.03, transparent: true, opacity: 0.35, depthWrite: false
        }));
        this.scene.add(this.dustMotes);

        // ── Lighting rig (warm + cozy) ──
        this.scene.add(new THREE.AmbientLight(0x3a3350, 0.55));

        const key = new THREE.DirectionalLight(0xffe6cf, 1.25);
        key.position.set(3, 5, 3);
        key.castShadow = true;
        key.shadow.mapSize.set(2048, 2048);
        key.shadow.camera.near = 0.5;
        key.shadow.camera.far = 14;
        key.shadow.camera.left = -4; key.shadow.camera.right = 4;
        key.shadow.camera.top = 4; key.shadow.camera.bottom = -2;
        key.shadow.bias = -0.0003; key.shadow.normalBias = 0.015;
        this.scene.add(key);

        const fill = new THREE.DirectionalLight(0xc9b8f2, 0.55);
        fill.position.set(-3, 2.5, 4.5);
        this.scene.add(fill);

        const rim = new THREE.DirectionalLight(0xb096ff, 0.7);
        rim.position.set(-2, 3, -3);
        this.scene.add(rim);

        // Warm bounce from the rug/lamp
        const warmBounce = new THREE.PointLight(0xff9f55, 0.35, 6);
        warmBounce.position.set(0, 0.5, 1.5);
        this.scene.add(warmBounce);
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
        const zDist = (size.y * 0.32) / Math.tan(rad);
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

    // ── Lip Sync (viseme-accurate, amplitude from real voice) ──
    _updateLipSync(delta) {
        if (!this.vrm?.expressionManager) return;

        const active = this.speaking && this.visemeSchedule && this.visemeSchedule.length > 0 && this.visemeDuration > 0;

        if (!active) {
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

        // Real voice amplitude (time-domain RMS) drives how open the mouth is.
        const rms = this._computeRMS();
        const gate = energyGate(rms, 0.012, 0.10);
        // Base 0.08 keeps a little life on consonants; vowels open fully with energy.
        const open = seg ? seg.open * (0.08 + 0.92 * gate) : 0;

        for (const [k, bs] of Object.entries(VOWEL_MAP)) {
            const target = (seg && seg.viseme === bs) ? open : 0;
            const r = 1 - Math.exp(-(target > this.smoothedVowels[bs] ? LIP_ATTACK : LIP_RELEASE) * delta);
            this.smoothedVowels[bs] += (target - this.smoothedVowels[bs]) * r;
            if (this.smoothedVowels[bs] < 0.004) this.smoothedVowels[bs] = 0;
            this.vrm.expressionManager.setValue(bs, this.smoothedVowels[bs]);
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

    // ── Natural gaze: soft lock to user + occasional saccades / looks around ──
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

        // If the user is typing, glance toward where they are (camera-left of her view).
        if (this.userTyping && this.gazeWander < 0.5) {
            this.gazePoint.x = lerp(this.gazePoint.x, this.defaultLookAt.x - 0.35, 0.08);
            this.gazePoint.y = lerp(this.gazePoint.y, this.defaultLookAt.y - 0.05, 0.08);
        }

        // Ease the fixation target toward the chosen gaze point.
        const lerpSpeed = 1 - Math.exp(-9 * delta);
        this.fixationTarget.lerp(this.gazePoint, lerpSpeed);
        // Tiny constant micro-drift so the eyes never feel frozen.
        this.fixationTarget.x += Math.sin(performance.now() * 0.0007) * 0.004;

        if (this.vrm.lookAt.target) {
            this.vrm.lookAt.target.position.copy(this.fixationTarget);
            this.vrm.lookAt.update(delta);
        }
    }

    // ── Expressions ──
    _ease(t) { return t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2; }

    _setEmotion(name, intensity = 1) {
        if (!this.vrm?.expressionManager) return;
        clearTimeout(this.expressionResetTimer);
        if (!Object.hasOwn(EMOTION_MAP, name)) return;
        const st = EMOTION_MAP[name];
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
        if (this.speaking || this.isTransitioning || this.restMode) return;

        this.microExpressionTimer += delta;
        if (this.microExpressionTimer >= this.nextMicroExpression) {
            this.microExpressionTimer = 0;
            this.nextMicroExpression = 5 + secureRand() * 7;
            const options = [
                { name: 'happy', value: secureRand() * 0.10 },
                { name: 'neutral', value: 0.85 + secureRand() * 0.15 },
            ];
            const chosen = options[Math.floor(secureRand() * options.length)];
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

    // ── Emotion posture + idle behaviors + rest state (layered on idle) ──
    _updateLife(delta, elapsed) {
        if (!this.vrm?.humanoid) return;
        const h = this.vrm.humanoid;

        // Decide rest mode from time since interaction.
        const since = (performance.now() - this.lastInteraction) / 1000;
        const wasRest = this.restMode;
        this.restMode = since > REST_IDLE_SECONDS;
        if (this.restMode !== wasRest) {
            this.breathScale = this.restMode ? 0.55 : 1.0;
        }
        if (!this.restMode) this.breathScale = lerp(this.breathScale, 1.0, 1 - Math.exp(-2 * delta));

        // Emotion-driven head posture targets.
        let tp = 0, ty = 0, tr = 0;
        switch (this.currentEmotion) {
            case 'happy': tp = -0.05; tr = 0.04; break;
            case 'sad': tp = 0.06; tr = -0.02; break;
            case 'surprised': tp = -0.07; break;
            case 'excited': tp = -0.04; tr = 0.03; break;
            case 'frustrated':
            case 'angry': tp = 0.02; tr = -0.05; break;
            case 'think': ty = 0.06; tp = -0.03; break;
        }
        if (this.agentStatus === 'thinking') { ty = 0.05; tp = -0.03; tr = 0.05; }
        if (this.restMode) { tp = lerp(tp, 0.05, 0.6); }

        // Occasional idle behavior (stretch / glance) when not speaking.
        if (!this.speaking) {
            this.idleBehaviorTimer -= delta;
            if (!this.idleBehavior && this.idleBehaviorTimer <= 0) {
                this.idleBehaviorTimer = this._rand(IDLE_BEHAVIOR_MIN, IDLE_BEHAVIOR_MAX);
                const kind = secureRand() < 0.5 ? 'stretch' : 'glance';
                this.idleBehavior = { kind, dur: kind === 'stretch' ? 2.2 : 1.6 };
                this.idleBehaviorT = 0;
            }
            if (this.idleBehavior) {
                this.idleBehaviorT += delta;
                const k = this.idleBehavior.kind;
                const p = this.idleBehaviorT / this.idleBehavior.dur;
                const env = Math.sin(Math.min(1, p) * Math.PI); // 0→1→0
                if (k === 'stretch') { tr += env * 0.06; tp -= env * 0.03; }
                else { ty += env * 0.12; }
                if (p >= 1) this.idleBehavior = null;
            }
        }

        // Smooth toward posture targets.
        const ps = 1 - Math.exp(-6 * delta);
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

        // Stretch raises the arms a bit (layered onto idle arm pose).
        if (this.idleBehavior && this.idleBehavior.kind === 'stretch') {
            const p = Math.min(1, this.idleBehaviorT / this.idleBehavior.dur);
            const env = Math.sin(p * Math.PI) * 0.5;
            const lu = h.getNormalizedBoneNode('leftUpperArm');
            const ru = h.getNormalizedBoneNode('rightUpperArm');
            if (lu) lu.rotation.z += env * 0.4;
            if (ru) ru.rotation.z -= env * 0.4;
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

    // ── Organic Idle Animation (absolute assignments, no drift) ──
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

        const sway = Math.sin(elapsed * 0.12) * Math.sin(elapsed * 0.20);
        const hipsNode = h.getNormalizedBoneNode('hips');
        if (hipsNode) {
            if (this.hipsBaseX === null) { this.hipsBaseX = hipsNode.position.x; this.hipsBaseY = hipsNode.position.y; this.hipsBaseZ = hipsNode.position.z; }
            hipsNode.position.x = this.hipsBaseX + sway * 0.005;
            hipsNode.rotation.z = sway * 0.003;
        }

        // Breathing scaled by life state (calmer in rest mode).
        const b = this.breathScale;
        const breath = Math.sin(elapsed * 0.80);
        const breath2 = Math.sin(elapsed * 0.80 + 0.35);
        setIdleRotation('spine', (breath * 0.006 + breath2 * 0.001) * b, 0, Math.sin(elapsed * 0.12) * 0.0008 * b);
        setIdleRotation('chest', (breath * 0.004) * b, 0, 0);

        const t = elapsed;
        const headPitch = Math.sin(t * 0.25 + 0.6) * 0.003 + Math.sin(t * 0.5) * 0.001;
        const headYaw = Math.sin(t * 0.18) * 0.005 + Math.sin(t * 0.35) * 0.002;
        const headRoll = Math.sin(t * 0.15 + 1.1) * 0.002;
        setIdleRotation('head', clamp(headPitch, -0.14, 0.14), clamp(headYaw, -0.21, 0.21), clamp(headRoll, -0.09, 0.09));
        setIdleRotation('neck', clamp(headPitch * 0.4, -0.08, 0.08), clamp(headYaw * 0.4, -0.12, 0.12), clamp(headRoll * 0.3, -0.05, 0.05));

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
            if (this.mixer) this.mixer.update(delta);
            this._updateIdle(elapsed, delta);
            this._updateLife(delta, elapsed);     // emotion posture + idle + rest (after idle)
            this._updateGaze(delta);              // gaze last so it owns the lookAt target

            this.vrm.humanoid?.update();
            this.vrm.springBoneManager?.update(delta);

            this._updateBlink(delta);
            this._updateExpressions(delta);
            this._updateMicroExpressions(delta);
            this._updateLipSync(delta);

            this.vrm.expressionManager?.update();
            this.vrm.nodeConstraintManager?.update();
        }

        if (this.dustMotes) this.dustMotes.rotation.y = elapsed * 0.01;

        this.renderer.render(this.scene, this.camera);
    }
}

window.CharacterRenderer = CharacterRenderer;
