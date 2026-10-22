// ─────────────────────────────────────────────────────────────────────────────
// Mai desktop overlay — web side.
//
// The native host (transparent/main.go) creates a small always-on-top window
// and hosts Edge WebView2 in it. The WebView2 is given a fully transparent
// background and this page paints nothing behind the avatar, so the desktop
// shows through everywhere the avatar does not cover.
//
// Everything below runs without a build step, a framework or a network call:
// plain ES modules served by the companion server.
// ─────────────────────────────────────────────────────────────────────────────

import * as THREE from 'three';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';
import { VRMLoaderPlugin, VRMUtils } from '@pixiv/three-vrm';
import { VRMAnimationLoaderPlugin, createVRMAnimationClip, VRMLookAtQuaternionProxy } from '@pixiv/three-vrm-animation';
import { selectWalkClipKey } from './walk-direction.js';

// ── Colour key ───────────────────────────────────────────────────────────────

// ── Camera / model ───────────────────────────────────────────────────────────
const CAMERA_FOV = 30;
const MODEL_URL = '/assets/mai.vrm';
const IDLE_VRMA_URL = '/assets/idle_loop.vrma';
// The VRMA_MotionPack clips are the only VRMA files in the repo that carry a
// specVersion, so they are the only ones three-vrm can load reliably. Each one
// is fetched lazily and a failure just disables that clip.
const ACTION_CLIP_URLS = [
    '/vrpack/VRMA_01.vrma',
    '/vrpack/VRMA_02.vrma',
    '/vrpack/VRMA_03.vrma',
    '/vrpack/VRMA_04.vrma',
    '/vrpack/VRMA_05.vrma',
    '/vrpack/VRMA_06.vrma',
    '/vrpack/VRMA_07.vrma',
];

// Imported directional walk clips. They are served from /assets alongside the
// idle loop and are registered as looping clips, never as one-shot actions.
const WALK_CLIP_URLS = {
    'walk-front': '/assets/walk-front.vrma',
    'walk-left-profile': '/assets/walk-left-profile.vrma',
    'walk-right-profile': '/assets/walk-right-profile.vrma',
    'walk-three-quarter-left': '/assets/walk-three-quarter-left.vrma',
    'walk-three-quarter-right': '/assets/walk-three-quarter-right.vrma',
};

// ── Timing ───────────────────────────────────────────────────────────────────
const FADE_MS = 400;
const FADE_MS_QUICK = 220;
const BLINK_CLOSE_DURATION = 0.08;
const BLINK_OPEN_MIN = 0.12;
const BLINK_OPEN_MAX = 0.25;
const BLINK_DELAY_MIN = 2.5;
const BLINK_DELAY_MAX = 6.0;
const DOUBLE_BLINK_CHANCE = 0.18;
const PARTIAL_BLINK_CHANCE = 0.25;

// ── Lip sync envelope (mirrors the tuned values in character.js) ─────────────
const LIP_ATTACK = 50;
const LIP_RELEASE = 30;
const LIP_CAP = 0.7;
const LIP_SILENCE_VOL = 0.04;
const LIP_IDLE_MS = 160;
const LIP_RELEASE_DURATION_MS = 200;

// ── Expressions actually present on mai.vrm (VRM 1.0, 14 of them) ───────────
// character.js also drives 'blush', 'think' and 'lookUp'; this model has none
// of those, so they are dropped here instead of silently doing nothing.
const VRM_EXPRESSIONS = [
    'happy', 'angry', 'sad', 'relaxed', 'surprised',
    'aa', 'ih', 'ou', 'ee', 'oh',
    'blink', 'blinkLeft', 'blinkRight', 'neutral',
];

const EMOTION_MAP = {
    happy:     { expression: [{ name: 'happy', value: 0.7 }], blendDuration: 0.4 },
    sad:       { expression: [{ name: 'sad', value: 0.65 }], blendDuration: 0.5 },
    angry:     { expression: [{ name: 'angry', value: 0.65 }], blendDuration: 0.3 },
    surprised: { expression: [{ name: 'surprised', value: 0.75 }, { name: 'aa', value: 0.12 }], blendDuration: 0.15 },
    neutral:   { expression: [{ name: 'neutral', value: 1.0 }], blendDuration: 0.6 },
    relaxed:   { expression: [{ name: 'relaxed', value: 0.6 }], blendDuration: 0.5 },
    calm:      { expression: [{ name: 'neutral', value: 1.0 }], blendDuration: 0.7 },
    stressed:  { expression: [{ name: 'angry', value: 0.5 }], blendDuration: 0.35 },
    excited:   { expression: [{ name: 'surprised', value: 0.6 }, { name: 'happy', value: 0.3 }, { name: 'aa', value: 0.12 }], blendDuration: 0.2 },
    frustrated:{ expression: [{ name: 'angry', value: 0.55 }, { name: 'sad', value: 0.12 }], blendDuration: 0.35 },
    tease:     { expression: [{ name: 'happy', value: 0.38 }], blendDuration: 0.3 },
    shy:       { expression: [{ name: 'happy', value: 0.25 }], blendDuration: 0.35 },
    touched:   { expression: [{ name: 'happy', value: 0.45 }, { name: 'relaxed', value: 0.2 }], blendDuration: 0.4 },
    skeptical: { expression: [{ name: 'surprised', value: 0.2 }], blendDuration: 0.3 },
    // 'think' is a character.js emotion, not a model expression: it becomes a
    // soft, wide-eyed look instead of a brow shape the model does not have.
    think:     { expression: [{ name: 'neutral', value: 0.85 }], blendDuration: 0.5 },
};

// Status → expression + body pose. The pose is applied to the head/spine
// bones so a status change reads even from across the desktop.
const STATUS_MAP = {
    idle:      { expression: [{ name: 'neutral', value: 1.0 }], pose: { pitch: 0, yaw: 0, roll: 0 } },
    thinking:  { expression: [{ name: 'neutral', value: 0.9 }], pose: { pitch: -0.12, yaw: 0.16, roll: 0.05 } },
    speaking:  { expression: [{ name: 'happy', value: 0.25 }], pose: { pitch: 0.04, yaw: 0, roll: 0 } },
    listening: { expression: [{ name: 'surprised', value: 0.18 }], pose: { pitch: -0.05, yaw: 0, roll: 0 } },
    error:     { expression: [{ name: 'sad', value: 0.45 }], pose: { pitch: 0.1, yaw: 0, roll: 0 } },
};

// Short desktop-automation gestures. They are head/arm overlays rather than
// full-body VRMA clips so they can run during a walk without interrupting the
// locomotion state machine.
const GESTURE_MAP = {
    click: {
        emotion: 'surprised', intensity: 0.45, durationMs: 650,
        pose: { pitch: -0.05, yaw: 0.08, roll: 0.03 }, restoreEmotion: true,
    },
    typing: {
        emotion: 'think', intensity: 0.55, durationMs: 1600,
        pose: { pitch: -0.08, yaw: -0.08, roll: 0.02 }, restoreEmotion: true,
    },
    not_found: {
        emotion: 'sad', intensity: 0.65, durationMs: 2200,
        pose: { pitch: 0.12, yaw: 0.0, roll: -0.05 }, restoreEmotion: false,
    },
    success: {
        emotion: 'happy', intensity: 0.6, durationMs: 1400,
        pose: { pitch: -0.04, yaw: 0.0, roll: 0.02 }, restoreEmotion: true,
    },
};

const VOWEL_MAP = { A: 'aa', E: 'ee', I: 'ih', O: 'oh', U: 'ou' };
const VOWEL_KEYS = ['A', 'E', 'I', 'O', 'U'];

// ── Locomotion presets ───────────────────────────────────────────────────────
// Five directional walk VRMA clips are loaded when available; the procedural
// cycle below is the fallback (and drives run/carry). Amplitudes are radians;
// frequencies are cycles per second.
//
// armLower: mai.vrm rests in a T-pose (the arms stick straight out), so the
// procedural layer has to bring the arms down before they can swing like a
// walking arm instead of a windmill.
const LOCOMOTION_PRESETS = {
    idle: {
        hz: 0,
        legSwing: 0, kneeBend: 0, armSwing: 0, armLower: 0,
        hipBob: 0, hipSway: 0, hipYaw: 0, lean: 0,
    },
    walk: {
        hz: 0.9,
        legSwing: 0.45, kneeBend: 0.75, armSwing: 0.28, armLower: 0.95,
        hipBob: 0.012, hipSway: 0.05, hipYaw: 0.06, lean: 0.03,
    },
    run: {
        hz: 1.8,
        legSwing: 0.88, kneeBend: 1.25, armSwing: 0.62, armLower: 0.95,
        hipBob: 0.028, hipSway: 0.09, hipYaw: 0.12, lean: 0.18,
    },
    carried: {
        hz: 0.35,
        legSwing: 0.10, kneeBend: 0.30, armSwing: 0.12, armLower: 0.55,
        hipBob: 0.004, hipSway: 0.03, hipYaw: 0.0, lean: -0.04,
    },
};

// Bones the procedural layer drives, straight on the VRM humanoid.
const RIG_BONES = [
    'hips',
    'leftUpperLeg', 'rightUpperLeg',
    'leftLowerLeg', 'rightLowerLeg',
    'leftFoot', 'rightFoot',
    'leftUpperArm', 'rightUpperArm',
];

// Sign of the humanoid bone X rotation that swings a limb forward. The VRM 1.0
// bone convention points the bone's local +Y at its parent, so a positive X
// rotation tips the limb backwards; verified against the loaded model.
const LIMB_FORWARD_SIGN = -1;

const clamp = (v, min, max) => Math.min(Math.max(v, min), max);
const clamp01 = (v) => clamp(v, 0, 1);
const lerp = (a, b, t) => a + (b - a) * t;

// Crypto-secure random, matching character.js (falls back to Math.random).
const secureRand = () => {
    const a = new Uint32Array(1);
    try { crypto.getRandomValues(a); return a[0] / 0x100000000; }
    catch (e) { return 0.5; }
};

const rand = (a, b) => a + secureRand() * (b - a);

// ─────────────────────────────────────────────────────────────────────────────
// Host bridge
// ─────────────────────────────────────────────────────────────────────────────
// go-webview2's edge package injects `window.external.invoke` before the page
// loads; `window.chrome.webview.postMessage` is the underlying primitive. Both
// are supported, and the page still works when neither exists (opened in a
// plain browser during development).
function hostSend(method, params) {
    const payload = JSON.stringify({ method, params: params || {} });
    try {
        // The host binds this through the webview package, which is the
        // supported path: it reaches the Go side as a normal function call.
        if (typeof window.hostCall === 'function') {
            window.hostCall(method, JSON.stringify(params || {}));
            return true;
        }
        if (window.external && typeof window.external.invoke === 'function') {
            window.external.invoke(payload);
            return true;
        }
        if (window.chrome && window.chrome.webview && typeof window.chrome.webview.postMessage === 'function') {
            window.chrome.webview.postMessage(payload);
            return true;
        }
    } catch (e) {
        console.warn('[VIRTUAL] host bridge failed', e);
    }
    return false;
}

// Events can arrive before the scene finished loading, so they are queued
// until the overlay is ready to handle them.
const pendingHostEvents = [];
let hostEventTarget = null;

function dispatchHostEvent(evt) {
    if (hostEventTarget) hostEventTarget(evt);
    else pendingHostEvents.push(evt);
}

// ─────────────────────────────────────────────────────────────────────────────
// Animation layer
// ─────────────────────────────────────────────────────────────────────────────
// A tiny clip registry on top of one AnimationMixer: `idle` loops forever,
// one-shot action clips fade in over idle and fade back out when they end,
// and the procedural locomotion slots (walk / run / carried) take over the
// humanoid bones directly.
class AnimationLayer {
    constructor(vrm, mixer) {
        this.vrm = vrm;
        this.mixer = mixer;
        this.entries = new Map();
        this.currentName = null;
        this.currentAction = null;
        this.oneShotNames = new Set();
        this.mixerFadedOut = false;

        mixer.addEventListener('finished', (e) => {
            const name = this._nameOf(e.action);
            if (name && this.oneShotNames.has(name)) {
                this.playClip('idle', { fadeMs: FADE_MS });
            }
        });
    }

    _nameOf(action) {
        for (const [name, entry] of this.entries) {
            if (entry.action === action) return name;
        }
        return null;
    }

    register(name, clip, { loop = true, fadeMs = FADE_MS, oneShot = false } = {}) {
        const action = this.mixer.clipAction(clip);
        action.setLoop(loop ? THREE.LoopRepeat : THREE.LoopOnce, Infinity);
        action.clampWhenFinished = !loop;
        // The nodes the clip drives: everything else keeps its rest pose.
        const animatedNodes = new Set(clip.tracks.map((t) => t.name.split('.')[0]));
        this.entries.set(name, { name, action, clip, loop, fadeMs, oneShot, animatedNodes });
        if (oneShot) this.oneShotNames.add(name);
        return action;
    }

    animatedNodes(name) {
        return this.entries.get(name)?.animatedNodes || new Set();
    }

    // Nodes the running actions are writing right now. A faded-out action
    // stops counting once its weight reaches zero, which is how the overlay
    // knows a bone has fallen back to the rest pose.
    drivenNodes() {
        const set = new Set();
        for (const entry of this.entries.values()) {
            const action = entry.action;
            if (!action.isRunning() || action.getEffectiveWeight() <= 0) continue;
            for (const name of entry.animatedNodes) set.add(name);
        }
        return set;
    }

    has(name) {
        return this.entries.has(name);
    }

    playClip(name, { fadeMs, warp = true, resetPose = true, startTime = null } = {}) {
        const entry = this.entries.get(name);
        if (!entry) return false;
        const action = entry.action;
        // Callers pass milliseconds, matching FADE_MS and resumeMixer.
        const dur = (fadeMs ?? entry.fadeMs) / 1000;
        const previous = this.currentAction;

        if (previous && previous !== action) {
            // Warping keeps clips in phase, but it distorts playback when the
            // clips have very different durations (idle 10s vs walk 4.2s).
            previous.crossFadeTo(action, dur, warp);
        }
        // three.js disables an action whose fade-out completed; play() alone
        // would not bring it back.
        action.enabled = true;
        action.reset();
        if (startTime != null && Number.isFinite(startTime)) {
            action.time = startTime;
        }
        action.setEffectiveWeight(1).fadeIn(dur).play();

        this.currentAction = action;
        this.currentName = name;
        this.mixerFadedOut = false;

        // A new clip must not inherit the previous clip's pose or gaze. A
        // walk→walk direction switch skips the reset so the gait does not pop.
        if (resetPose) {
            this.vrm.humanoid?.resetNormalizedPose();
            this.vrm.lookAt?.reset();
        }
        if (this.vrm.lookAt) {
            this.vrm.lookAt.autoUpdate = (entry.clip.lookAtTrack != null);
        }
        return true;
    }

    // Fade the mixer layer out so the procedural layer owns the bones.
    fadeMixerOut(durationMs = FADE_MS) {
        if (!this.currentAction) return;
        this.currentAction.fadeOut(durationMs / 1000);
        this.mixerFadedOut = true;
    }

    // Bring the mixer layer back in (idle clip) after a procedural state.
    resumeMixer(name = 'idle', durationMs = FADE_MS) {
        const entry = this.entries.get(name);
        if (!entry) return false;
        const action = entry.action;
        const dur = durationMs / 1000;
        if (this.currentAction && this.currentAction !== action) {
            this.currentAction.crossFadeTo(action, dur, true);
        }
        action.enabled = true;
        action.setEffectiveWeight(1).fadeIn(dur).play();
        this.currentAction = action;
        this.currentName = name;
        this.mixerFadedOut = false;
        if (this.vrm.lookAt) {
            this.vrm.lookAt.autoUpdate = (entry.clip.lookAtTrack != null);
        }
        return true;
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// Procedural locomotion
// ─────────────────────────────────────────────────────────────────────────────
// Drives the normalized humanoid bones directly. The mixer may also write
// those bones, so every channel keeps the "base" pose the mixer left behind
// and composes the procedural offset on top of it. The base is only replaced
// when the mixer actually wrote a new value, so the offsets never accumulate.
class ProceduralRig {
    constructor(vrm) {
        this.vrm = vrm;
        this.mode = 'idle';
        this.phase = 0;
        this.time = 0;
        this.channels = new Map();
        this._offsets = new Map();
        this._quat = new THREE.Quaternion();
        this._pose = new THREE.Quaternion();
        this._axisZ = new THREE.Vector3(0, 0, 1);
        this._euler = new THREE.Euler();
        this._vec = new THREE.Vector3();
    }

    _bone(name) {
        return this.vrm.humanoid?.getNormalizedBoneNode(name) || null;
    }

    // Capture the rest (T) pose. Procedural motion is expressed relative to it
    // so the cycle looks the same no matter what the mixer was doing.
    captureRest() {
        for (const name of RIG_BONES) {
            const node = this._bone(name);
            if (!node) continue;
            const child = node.children.find((c) => c.isObject3D);
            // Direction to the child bone, in the bone's own frame. The arms
            // point sideways in mai.vrm, the legs point down, and the two need
            // opposite corrections to hang and swing correctly.
            const dirLocal = child
                ? child.position.clone().normalize()
                : new THREE.Vector3(0, -1, 0);
            this.channels.set(name, {
                node,
                dirLocal,
                // Left arm points +X, right arm -X: lowering means the opposite
                // rotation about the bone's local Z.
                armSign: -Math.sign(dirLocal.x) || 1,
                baseQuat: node.quaternion.clone(),
                restQuat: node.quaternion.clone(),
                basePos: node.position.clone(),
                restPos: node.position.clone(),
                lastQuat: node.quaternion.clone(),
                lastPos: node.position.clone(),
            });
        }
    }

    setMode(mode) {
        if (!LOCOMOTION_PRESETS[mode] || mode === this.mode) return false;
        this.mode = mode;
        return true;
    }

    // The nodes the mixer drives. Everything else has to be put back into the
    // rest pose by hand, otherwise a bone the clip does not touch would keep
    // the last procedural pose forever.
    setMixerNodes(names) {
        this.mixerNodes = names;
    }

    restoreRestPose() {
        for (const ch of this.channels.values()) {
            if (this.mixerNodes && this.mixerNodes.has(ch.node.name)) continue;
            ch.node.quaternion.copy(ch.restQuat);
            ch.node.position.copy(ch.restPos);
            ch.lastQuat.copy(ch.restQuat);
            ch.lastPos.copy(ch.restPos);
        }
    }

    // Called after mixer.update(): adopt whatever the mixer just wrote.
    syncFromMixer() {
        for (const ch of this.channels.values()) {
            if (ch.node.quaternion.angleTo(ch.lastQuat) > 1e-4) {
                ch.baseQuat.copy(ch.node.quaternion);
            }
            if (ch.node.position.distanceToSquared(ch.lastPos) > 1e-10) {
                ch.basePos.copy(ch.node.position);
            }
        }
    }

    // Snap the base back to the rest pose (used when the mixer stops driving
    // a bone, e.g. while the idle action is faded out for locomotion).
    useRestBase() {
        for (const ch of this.channels.values()) {
            ch.baseQuat.copy(ch.restQuat);
            ch.basePos.copy(ch.restPos);
        }
    }

    update(delta) {
        const preset = LOCOMOTION_PRESETS[this.mode] || LOCOMOTION_PRESETS.idle;
        this.time += delta;
        this.phase = (this.phase + delta * preset.hz * Math.PI * 2) % (Math.PI * 2);

        const p = this.phase;
        const s = Math.sin(p);

        // A positive rotation about a limb's local X swings it backwards, so
        // the sign is inverted to get "forward" out of the phase.
        const leftLeg = LIMB_FORWARD_SIGN * s * preset.legSwing;
        const rightLeg = -leftLeg;
        // The knee only folds during the swing phase, and it folds backwards.
        const leftKnee = Math.max(0, -Math.sin(p - 0.7)) * preset.kneeBend;
        const rightKnee = Math.max(0, -Math.sin(p - 0.7 + Math.PI)) * preset.kneeBend;
        // The foot counter-rotates to stay level, with a little toe roll.
        const leftFoot = -leftLeg * 0.85 - 0.18 * Math.max(0, s) + 0.25 * Math.max(0, -s);
        const rightFoot = -rightLeg * 0.85 - 0.18 * Math.max(0, -s) + 0.25 * Math.max(0, s);

        this._apply('leftUpperLeg', { x: leftLeg });
        this._apply('rightUpperLeg', { x: rightLeg });
        this._apply('leftLowerLeg', { x: leftKnee });
        this._apply('rightLowerLeg', { x: rightKnee });
        this._apply('leftFoot', { x: leftFoot });
        this._apply('rightFoot', { x: rightFoot });

        // Arms swing opposite to the legs and are lowered out of the T-pose.
        const armSwing = s * preset.armSwing;
        this._apply('leftUpperArm', { x: armSwing }, null, 'arm');
        this._apply('rightUpperArm', { x: -armSwing }, null, 'arm');

        // Hips: vertical bob at twice the step rate, side sway and yaw at the
        // step rate, plus a forward lean that grows with speed.
        this._apply('hips', {
            x: preset.lean,
            y: Math.sin(p) * preset.hipYaw,
            z: s * preset.hipSway,
        }, {
            y: Math.abs(Math.sin(p)) * preset.hipBob,
        });
    }

    // Write a rotation offset for one bone, composed onto the mixer's base.
    // `pose` is an extra rest-pose correction applied inside the swing.
    _apply(name, euler, position, poseKind) {
        const ch = this.channels.get(name);
        if (!ch) return;
        this._euler.set(euler.x || 0, euler.y || 0, euler.z || 0, 'XYZ');
        this._quat.setFromEuler(this._euler);
        if (poseKind === 'arm') {
            // Lower the arm out of the T-pose: a rotation about the bone's
            // local Z, applied before the swing.
            const preset = LOCOMOTION_PRESETS[this.mode] || LOCOMOTION_PRESETS.idle;
            this._pose.setFromAxisAngle(this._axisZ, ch.armSign * preset.armLower);
            this._quat.multiply(this._pose);
        }
        ch.node.quaternion.copy(ch.baseQuat).multiply(this._quat);
        if (position) {
            this._vec.set(position.x || 0, position.y || 0, position.z || 0);
            ch.node.position.copy(ch.basePos).add(this._vec);
        }
        ch.lastQuat.copy(ch.node.quaternion);
        ch.lastPos.copy(ch.node.position);
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// Overlay
// ─────────────────────────────────────────────────────────────────────────────
class VirtualOverlay {
    constructor(container) {
        this.container = container;
        this.scene = null;
        this.camera = null;
        this.renderer = null;
        this.vrm = null;
        this.mixer = null;
        this.layer = null;
        this.rig = null;
        this.clock = { last: performance.now() };
        this.loaded = false;

        // Animation state
        this.locomotion = 'idle';
        this.locomotionAuto = true;
        this.actionClips = new Map();
        this.pendingClip = null;
        this.walkClipKey = 'walk-front';
        this.vrmaLocomotion = false;
        this.hostWalking = false;
        this.walkDir = null;

        // Expression state
        this.currentExpressionValues = new Map();
        this.targetExpressionValues = new Map();
        this.isTransitioning = false;
        this.transitionProgress = 0;
        this.currentEmotion = null;
        this.expressionResetTimer = null;
        this.agentStatus = 'idle';
        this.statusPose = { pitch: 0, yaw: 0, roll: 0 };
        this.statusPoseTarget = { pitch: 0, yaw: 0, roll: 0 };
        this.gesturePose = { pitch: 0, yaw: 0, roll: 0 };
        this.gesturePoseTarget = { pitch: 0, yaw: 0, roll: 0 };
        this.gestureName = null;
        this.gestureStart = 0;
        this.gestureDuration = 0;
        this.gestureTimer = null;
        this.blinkState = null;

        // Head/neck overlay state. Status and gesture offsets are composed on
        // top of the pose the mixer (or rest) owns this frame, never added to
        // the bone's current rotation, so they cannot accumulate.
        this._headBase = new THREE.Quaternion();
        this._neckBase = new THREE.Quaternion();
        this._statusDelta = new THREE.Quaternion();
        this._neckDelta = new THREE.Quaternion();
        this._statusEuler = new THREE.Euler();

        // Lip sync state
        this.speaking = false;
        this.analyser = null;
        this.timeData = null;
        this.smoothedVowels = { aa: 0, ee: 0, ih: 0, oh: 0, ou: 0 };
        this.lipEnv = 0;
        this.lipRelease = { remainingMs: 0, lastForcedValue: 0 };
        this.visemeSchedule = null;
        this.visemeDuration = 0;
        this.speechClock = 0;
        this.fallbackVowel = 0;

        // Audio / websocket
        this.audio = null;
        this.ws = null;
        this.pendingAudio = [];

        // Drag state
        this.dragging = false;
        this.hostDragging = false;
        this.dragLast = { x: 0, y: 0 };
        this.clickThrough = false;
        this.lastMoveEventAt = 0;
        this.moveIdleTimer = null;
    }

    // ── Setup ────────────────────────────────────────────────────────────────
    init() {
        this._initScene();
        this._initRenderer();
        this._initInput();
        this._initWebSocket();
        this._animate();
    }

    _initRenderer() {
        // The canvas is drawn with a per-pixel alpha and the page behind it is
        // transparent, so the host can composite the window against the
        // desktop. The host gives the WebView2 a transparent background of its
        // own; a colour key cannot be used, because the WebView2 renders into
        // a DirectComposition surface that the window manager composites
        // separately from the host window.
        this.renderer = new THREE.WebGLRenderer({
            antialias: true,
            alpha: true,
            premultipliedAlpha: true,
            powerPreference: 'high-performance',
        });
        this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
        this.renderer.setClearColor(0x000000, 0);
        this.renderer.outputColorSpace = THREE.SRGBColorSpace;
        this.renderer.toneMapping = THREE.LinearToneMapping;
        this.renderer.toneMappingExposure = 0.88;
        this.renderer.shadowMap.enabled = false;
        this.container.appendChild(this.renderer.domElement);

        this._resize();
        window.addEventListener('resize', () => this._resize());
        if (window.ResizeObserver && this.container) {
            new ResizeObserver(() => this._resize()).observe(this.container);
        }
    }

    _initScene() {
        this.scene = new THREE.Scene();
        this.camera = new THREE.PerspectiveCamera(CAMERA_FOV, 1, 0.01, 100);

        // Same three-point rig as the main app, so the avatar looks identical
        // in the overlay and in the normal web UI.
        this.scene.add(new THREE.AmbientLight(0xffe8d4, 0.30));
        const keyLight = new THREE.DirectionalLight(0xffedda, 0.85);
        keyLight.position.set(1.1, 1.6, 1.4);
        this.scene.add(keyLight);
        const fillLight = new THREE.DirectionalLight(0xd6dff0, 0.22);
        fillLight.position.set(-1.2, 0.6, 0.9);
        this.scene.add(fillLight);
        const rimLight = new THREE.DirectionalLight(0xfff2e0, 0.25);
        rimLight.position.set(-0.6, 1.0, -1.6);
        this.scene.add(rimLight);
    }

    _resize() {
        const w = this.container.clientWidth || window.innerWidth;
        const h = this.container.clientHeight || window.innerHeight;
        if (w === 0 || h === 0) return;
        this.renderer.setSize(w, h, false);
        this.camera.aspect = w / h;
        this.camera.updateProjectionMatrix();
        this._frameModel();
    }

    // ── VRM loading ─────────────────────────────────────────────────────────
    _loadModel() {
        const loader = new GLTFLoader();
        loader.register((parser) => new VRMLoaderPlugin(parser));
        loader.register((parser) => new VRMAnimationLoaderPlugin(parser));

        const load = (url) => new Promise((resolve, reject) => {
            loader.load(url, resolve, undefined, reject);
        });

        Promise.all([load(MODEL_URL), load(IDLE_VRMA_URL)])
            .then(async ([vrmGltf, vrmaGltf]) => {
                const vrm = vrmGltf.userData?.vrm;
                if (!vrm) {
                    console.error('[VIRTUAL] no VRM data in', MODEL_URL);
                    return;
                }
                this.vrm = vrm;

                VRMUtils.removeUnnecessaryVertices(vrm.scene);
                VRMUtils.combineSkeletons(vrm.scene);
                this._tuneMaterials(vrm);

                this.scene.add(vrm.scene);
                vrm.scene.updateMatrixWorld(true);

                vrm.springBoneManager?.reset();
                if (vrm.springBoneManager) {
                    vrm.springBoneManager.colliderGroups.forEach((group) => {
                        group.colliders.forEach((collider) => {
                            if (collider.radius) collider.radius *= 0.9;
                        });
                    });
                    vrm.springBoneManager.joints.forEach((joint) => {
                        if (joint.settings) {
                            joint.settings.stiffness *= 0.6;
                            joint.settings.dragForce *= 0.85;
                            joint.settings.gravityPower *= 1.3;
                        }
                    });
                }

                if (!vrm.lookAt.target) {
                    vrm.lookAt.target = new THREE.Object3D();
                }
                this.scene.add(vrm.lookAt.target);

                // createVRMAnimationClip looks for this proxy on the scene and
                // creates (and warns about) one when it is missing.
                if (!vrm.scene.children.find((o) => o instanceof VRMLookAtQuaternionProxy)) {
                    const proxy = new VRMLookAtQuaternionProxy(vrm.lookAt);
                    proxy.name = 'VRMLookAtQuaternionProxy';
                    vrm.scene.add(proxy);
                }

                // Remember the neutral neck/head pose. The imported walk
                // clips come from a different skeleton, and their neck/head
                // channels can fold the head onto the chest. The walk branch
                // restores this pose after the mixer runs.
                this.neckRest = vrm.humanoid?.getNormalizedBoneNode('neck')?.quaternion.clone() || null;
                this.headRest = vrm.humanoid?.getNormalizedBoneNode('head')?.quaternion.clone() || null;
                if (this.neckRest) this._neckBase.copy(this.neckRest);
                if (this.headRest) this._headBase.copy(this.headRest);

                this.mixer = new THREE.AnimationMixer(vrm.scene);
                this.layer = new AnimationLayer(vrm, this.mixer);
                this.rig = new ProceduralRig(vrm);
                this.rig.captureRest();
                // Idle loop
                try {
                    const anims = vrmaGltf?.userData?.vrmAnimations;
                    if (anims && anims.length > 0) {
                        const anim = anims[0];
                        const clip = createVRMAnimationClip(anim, vrm);
                        // Keep the authored look-at on the clip: the animation
                        // layer decides from it whether the gaze system runs.
                        clip.lookAtTrack = anim.lookAtTrack;
                        this.layer.register('idle', clip, { loop: true, fadeMs: FADE_MS });
                        this.layer.playClip('idle', { fadeMs: FADE_MS });
                        this.rig.setMixerNodes(this.layer.animatedNodes('idle'));
                    } else {
                        console.warn('[VIRTUAL] idle VRMA has no animation');
                    }
                } catch (e) {
                    console.error('[VIRTUAL] idle VRMA error:', e);
                }

                // Directional walk clips are optional: a failed file simply
                // falls back to the procedural walk cycle.
                await this._loadWalkClips(loader, vrm, load);

                this._frameModel();
                this.loaded = true;
                console.log('[VIRTUAL] ready');
                this._logState();
            })
            .catch((err) => console.error('[VIRTUAL] load error:', err));
    }

    async _loadWalkClips(loader, vrm, load) {
        let loaded = 0;
        await Promise.all(Object.entries(WALK_CLIP_URLS).map(async ([name, url]) => {
            try {
                const gltf = await load(url);
                const anim = gltf?.userData?.vrmAnimations?.[0];
                if (!anim) throw new Error('no vrmAnimations');
                const clip = createVRMAnimationClip(anim, vrm);
                clip.lookAtTrack = anim.lookAtTrack;
                this.layer.register(name, clip, { loop: true, fadeMs: FADE_MS_QUICK });
                loaded++;
            } catch (e) {
                console.warn('[VIRTUAL] walk clip failed', name, e);
            }
        }));
        if (!this.layer.has(this.walkClipKey)) {
            this.walkClipKey = this.layer.has('walk-front') ? 'walk-front' : null;
        }
        console.log(`[VIRTUAL] walk clips ready: ${loaded}/${Object.keys(WALK_CLIP_URLS).length}`);
    }

    // _logState reports the sizes that decide whether anything is on screen. The
    // host forwards the console to its own log, so when the overlay shows an
    // empty window this is what says why: a zero-sized canvas, a camera that
    // does not frame the model, or a renderer that never drew.
    _logState() {
        const canvas = this.renderer?.domElement;
        const size = this.renderer?.getSize(new THREE.Vector2());
        const box = new THREE.Box3().setFromObject(this.vrm.scene);
        const sphere = box.getBoundingSphere(new THREE.Sphere());
        console.log('[VIRTUAL] state', JSON.stringify({
            container: [this.container?.clientWidth, this.container?.clientHeight],
            window: [window.innerWidth, window.innerHeight],
            canvasCss: canvas ? [canvas.clientWidth, canvas.clientHeight] : null,
            canvasBuffer: canvas ? [canvas.width, canvas.height] : null,
            rendererSize: size ? [size.x, size.y] : null,
            pixelRatio: this.renderer?.getPixelRatio(),
            camera: this.camera ? {
                pos: this.camera.position.toArray().map((v) => +v.toFixed(3)),
                fov: this.camera.fov,
                aspect: +this.camera.aspect.toFixed(3),
            } : null,
            modelCentre: sphere.center.toArray().map((v) => +v.toFixed(3)),
            modelRadius: +sphere.radius.toFixed(3),
            drawCalls: this.renderer?.info.render.calls,
            triangles: this.renderer?.info.render.triangles,
        }));
    }

    // three-vrm swaps every MToon material for an [surface, outline] pair and
    // the faceting in the main app came from a missing MSAA, not the material,
    // so the VRoid shading parameters are left exactly as exported.
    _tuneMaterials(vrm) {
        const maxAnisotropy = this.renderer.capabilities.getMaxAnisotropy();
        vrm.scene.traverse((obj) => {
            obj.frustumCulled = false;
            if (!obj.isMesh || !obj.material) return;
            const materials = Array.isArray(obj.material) ? obj.material : [obj.material];
            for (const mat of materials) {
                if (mat.map) mat.map.anisotropy = Math.min(8, maxAnisotropy);
                if (!mat.isMToonMaterial) continue;
                if (mat.flatShading) {
                    mat.flatShading = false;
                    mat.needsUpdate = true;
                }
            }
        });
    }

    // ── Camera framing ──────────────────────────────────────────────────────
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
        if (box.isEmpty()) return;

        const size = new THREE.Vector3();
        const center = new THREE.Vector3();
        box.getSize(size);
        box.getCenter(center);

        // Fit the whole model in the viewport, biased slightly towards the
        // head so the face stays readable in a small window.
        const halfFov = (CAMERA_FOV / 2) * Math.PI / 180;
        const distH = (size.y / 2) / Math.tan(halfFov);
        const distW = (size.x / 2) / (Math.tan(halfFov) * Math.max(this.camera.aspect, 0.1));
        const dist = Math.max(distH, distW) * 1.06;

        const lookY = center.y + size.y * 0.12;
        this.camera.position.set(center.x, lookY, center.z + dist);
        this.camera.lookAt(center.x, lookY, center.z);

        // The gaze target sits straight in front of her head, so the look-at
        // system keeps her eyes on the viewer instead of the scene origin.
        const head = this.vrm.humanoid?.getNormalizedBoneNode('head');
        if (head && this.vrm.lookAt?.target) {
            head.updateMatrixWorld(true);
            const headPos = head.getWorldPosition(new THREE.Vector3());
            this.vrm.lookAt.target.position.set(headPos.x, headPos.y, headPos.z + 1.5);
        }
    }

    // ── Animation state machine ─────────────────────────────────────────────
    setLocomotion(mode, { auto = false, dx = null, dy = null } = {}) {
        if (!LOCOMOTION_PRESETS[mode]) return false;
        if (!auto) this.locomotionAuto = false;

        if (mode === 'walk') {
            if (dx !== null || dy !== null) this._selectWalkClip(dx || 0, dy || 0);
            if (this.walkClipKey && this.layer?.has(this.walkClipKey)) {
                const already = this.locomotion === 'walk' && this.vrmaLocomotion &&
                    this.layer.currentName === this.walkClipKey;
                if (already) return true;

                // If the procedural layer faded the mixer out, bring idle back
                // first. Crossfading from a zero-weight action can expose a
                // one-frame T-pose.
                if (this.layer.mixerFadedOut) {
                    this.layer.resumeMixer('idle', FADE_MS_QUICK);
                }
                this.rig.useRestBase();

                const previous = this.layer.currentAction;
                const previousTime = previous ? previous.time : 0;
                const clipDuration = this.layer.entries.get(this.walkClipKey)?.clip.duration || 0;
                const switchingWalk = this.vrmaLocomotion &&
                    String(this.layer.currentName || '').startsWith('walk-');
                this.layer.playClip(this.walkClipKey, {
                    fadeMs: FADE_MS_QUICK,
                    warp: false,
                    resetPose: !this.vrmaLocomotion,
                    startTime: switchingWalk && clipDuration > 0
                        ? previousTime % clipDuration
                        : null,
                });
                this.rig.setMixerNodes(this.layer.animatedNodes(this.walkClipKey));
                this.rig.setMode('idle');
                this.vrmaLocomotion = true;
                this.locomotion = mode;
                console.log('[VIRTUAL] locomotion: walk', this.walkClipKey);
                return true;
            }
            // No walk clips loaded (or all failed): use the procedural cycle.
        }

        if (this.locomotion === mode && !this.vrmaLocomotion) return true;
        this.locomotion = mode;
        this.vrmaLocomotion = false;

        if (mode === 'idle') {
            // Hand the bones back to the mixer (the idle clip).
            this.rig.useRestBase();
            this.layer.resumeMixer('idle', FADE_MS_QUICK);
            this.rig.setMixerNodes(this.layer.animatedNodes('idle'));
        } else {
            // Procedural locomotion owns the bones: fade the mixer out and let
            // the rig blend the offsets in.
            this.layer.fadeMixerOut(FADE_MS_QUICK);
            this.rig.useRestBase();
        }
        this.rig.setMode(mode);
        console.log('[VIRTUAL] locomotion:', mode);
        return true;
    }

    // Play a one-shot action clip (lazy loaded from the VRMA motion pack).
    playAction(name) {
        const key = String(name || '').toLowerCase().trim();
        if (!key) return false;

        // Automation gestures are overlays, not full-body clips.
        if (GESTURE_MAP[key]) return this.playGesture(key);

        if (this.layer.has(key)) {
            this._startOneShot(key);
            return true;
        }
        const url = ACTION_CLIP_URLS.find((u) => u.toLowerCase().includes(key)) || null;
        if (!url) {
            console.warn('[VIRTUAL] no action clip for', key);
            return false;
        }
        if (this.actionClips.has(url)) {
            this._startOneShot(this.actionClips.get(url));
            return true;
        }
        // Load on first use; a broken clip is simply skipped.
        this.pendingClip = url;
        const loader = new GLTFLoader();
        loader.register((parser) => new VRMAnimationLoaderPlugin(parser));
        loader.load(url, (gltf) => {
            try {
                const anims = gltf?.userData?.vrmAnimations;
                if (!anims || !anims.length) throw new Error('no vrmAnimations');
                const clip = createVRMAnimationClip(anims[0], this.vrm);
                clip.lookAtTrack = anims[0].lookAtTrack;
                this.layer.register(url, clip, { loop: false, fadeMs: FADE_MS, oneShot: true });
                this.actionClips.set(url, url);
                if (this.pendingClip === url) {
                    this.pendingClip = null;
                    this._startOneShot(url);
                }
            } catch (e) {
                console.warn('[VIRTUAL] action clip failed', url, e);
            }
        }, undefined, (err) => {
            console.warn('[VIRTUAL] action clip not available', url, err);
        });
        return true;
    }

    _startOneShot(name) {
        if (this.locomotion !== 'idle' && this.locomotionAuto) {
            this.setLocomotion('idle', { auto: true });
        }
        this.layer.playClip(name, { fadeMs: FADE_MS });
    }

    // A small, non-destructive gesture layer. The mixer keeps owning the walk
    // or idle pose; these offsets are added after it each frame.
    playGesture(name) {
        const key = String(name || '').toLowerCase().trim();
        const cfg = GESTURE_MAP[key];
        if (!cfg) return false;

        clearTimeout(this.gestureTimer);
        this.gestureName = key;
        this.gestureStart = performance.now();
        this.gestureDuration = cfg.durationMs;
        this.gesturePoseTarget = { ...cfg.pose };
        this.setEmotion(cfg.emotion, cfg.intensity);

        this.gestureTimer = setTimeout(() => {
            this.gestureName = null;
            this.gesturePoseTarget = { pitch: 0, yaw: 0, roll: 0 };
            if (cfg.restoreEmotion && !this.speaking) {
                this.setEmotion('neutral', 0.8);
            }
        }, cfg.durationMs);
        return true;
    }

    // ── Expressions ─────────────────────────────────────────────────────────
    setEmotion(emotion, intensity = 1) {
        const name = EMOTION_MAP[emotion] ? emotion : 'calm';
        this._setEmotion(name, intensity);
        clearTimeout(this.expressionResetTimer);
        if (!this.speaking) {
            this.expressionResetTimer = setTimeout(() => this._setEmotion('neutral', 1), 4000);
        }
    }

    _setEmotion(name, intensity = 1) {
        if (!this.vrm?.expressionManager) return;
        const st = EMOTION_MAP[name];
        if (!st) return;
        this.currentEmotion = name;
        this.isTransitioning = true;
        this.transitionProgress = 0;

        // Capture the current values so the blend starts where the face is now.
        this.currentExpressionValues.clear();
        this.targetExpressionValues.clear();
        const map = this.vrm.expressionManager.expressionMap || this.vrm.expressionManager._expressionMap;
        if (map) {
            for (const n of Object.keys(map)) {
                this.currentExpressionValues.set(n, this.vrm.expressionManager.getValue(n) || 0);
                this.targetExpressionValues.set(n, 0);
            }
        }
        const norm = clamp01(intensity);
        for (const e of st.expression) {
            if (!VRM_EXPRESSIONS.includes(e.name)) continue;
            this.targetExpressionValues.set(e.name, e.value * norm);
        }
    }

    _updateExpressions(delta) {
        if (!this.isTransitioning || !this.currentEmotion || !this.vrm?.expressionManager) return;
        const dur = EMOTION_MAP[this.currentEmotion]?.blendDuration || 0.3;
        this.transitionProgress += delta / dur;
        if (this.transitionProgress >= 1) {
            this.transitionProgress = 1;
            this.isTransitioning = false;
        }
        const eased = this.transitionProgress < 0.5
            ? 4 * this.transitionProgress ** 3
            : 1 - Math.pow(-2 * this.transitionProgress + 2, 3) / 2;
        for (const [n, t] of this.targetExpressionValues) {
            const s = this.currentExpressionValues.get(n) || 0;
            this.vrm.expressionManager.setValue(n, s + (t - s) * eased);
        }
    }

    setStatus(status) {
        this.agentStatus = status || 'idle';
        const cfg = STATUS_MAP[this.agentStatus];
        if (!cfg) return;
        this.statusPoseTarget = cfg.pose;
        // The status expression is a gentle baseline; emotions override it.
        for (const e of cfg.expression) {
            if (!VRM_EXPRESSIONS.includes(e.name)) continue;
            if (this.currentEmotion && EMOTION_MAP[this.currentEmotion]) continue;
            this.vrm?.expressionManager?.setValue(e.name, e.value);
        }
    }

    _updateStatusPose(delta) {
        const rate = 1 - Math.exp(-6 * delta);
        this.statusPose.pitch = lerp(this.statusPose.pitch, this.statusPoseTarget.pitch, rate);
        this.statusPose.yaw = lerp(this.statusPose.yaw, this.statusPoseTarget.yaw, rate);
        this.statusPose.roll = lerp(this.statusPose.roll, this.statusPoseTarget.roll, rate);

        const gestureRate = 1 - Math.exp(-12 * delta);
        this.gesturePose.pitch = lerp(this.gesturePose.pitch, this.gesturePoseTarget.pitch, gestureRate);
        this.gesturePose.yaw = lerp(this.gesturePose.yaw, this.gesturePoseTarget.yaw, gestureRate);
        this.gesturePose.roll = lerp(this.gesturePose.roll, this.gesturePoseTarget.roll, gestureRate);
    }

    // ── Blink ───────────────────────────────────────────────────────────────
    _updateBlink(delta) {
        if (!this.vrm?.expressionManager) return;
        const em = this.vrm.expressionManager;
        const dtMs = delta * 1000;

        if (!this.blinkState) {
            this.blinkState = {
                phase: 'idle',
                progress: 0,
                delayMs: rand(BLINK_DELAY_MIN, BLINK_DELAY_MAX) * 1000,
                openDurationMs: rand(BLINK_OPEN_MIN, BLINK_OPEN_MAX) * 1000,
                isPartial: false,
                partialIntensity: 1,
                wink: 0,
            };
        }
        const bs = this.blinkState;
        const easeOutSine = (t) => Math.sin((t * Math.PI) / 2);
        const easeInSine = (t) => 1 - Math.cos((t * Math.PI) / 2);

        const curBlink = em.getValue('blink') ?? 0;
        const curHappy = em.getValue('happy') ?? 0;
        if (bs.phase === 'idle' && (curBlink > 0.4 || curHappy > 0.6)) {
            bs.delayMs = rand(BLINK_DELAY_MIN, BLINK_DELAY_MAX) * 1000;
            return;
        }

        if (bs.phase === 'idle') {
            bs.delayMs -= dtMs;
            if (bs.delayMs <= 0) {
                bs.phase = 'closing';
                bs.progress = 0;
                bs.isPartial = secureRand() < PARTIAL_BLINK_CHANCE;
                bs.partialIntensity = bs.isPartial ? 0.4 + secureRand() * 0.3 : 1;
                // An occasional wink uses the one-sided expressions the model has.
                bs.wink = secureRand() < 0.15 ? (secureRand() < 0.5 ? -1 : 1) : 0;
                bs.openDurationMs = rand(BLINK_OPEN_MIN, BLINK_OPEN_MAX) * 1000;
            }
            return;
        }

        if (bs.phase === 'closing') {
            bs.progress = Math.min(1, bs.progress + dtMs / (BLINK_CLOSE_DURATION * 1000));
            const amount = bs.partialIntensity * easeOutSine(bs.progress);
            em.setValue('blink', amount);
            if (bs.wink < 0) em.setValue('blinkLeft', amount);
            if (bs.wink > 0) em.setValue('blinkRight', amount);
            if (bs.progress >= 1) {
                bs.phase = 'opening';
                bs.progress = 0;
            }
            return;
        }

        bs.progress = Math.min(1, bs.progress + dtMs / bs.openDurationMs);
        const amount = bs.partialIntensity * (1 - easeInSine(bs.progress));
        em.setValue('blink', amount);
        if (bs.wink < 0) em.setValue('blinkLeft', amount);
        if (bs.wink > 0) em.setValue('blinkRight', amount);

        if (bs.progress >= 1) {
            bs.phase = 'idle';
            bs.progress = 0;
            em.setValue('blink', 0);
            em.setValue('blinkLeft', 0);
            em.setValue('blinkRight', 0);
            bs.delayMs = rand(BLINK_DELAY_MIN, BLINK_DELAY_MAX) * 1000;
            if (!bs.isPartial && secureRand() < DOUBLE_BLINK_CHANCE) {
                bs.delayMs = 120;
            }
        }
    }

    // ── Lip sync ────────────────────────────────────────────────────────────
    // The Go side plays the audio locally, so the overlay only mirrors the
    // waveform: the AudioPlayer is muted, its analyser drives how wide the
    // mouth opens, and the viseme schedule (built from the reply text) picks
    // the vowel shape.
    setAnalyser(analyser) {
        this.analyser = analyser;
        if (analyser) this.timeData = new Uint8Array(analyser.fftSize);
    }

    prepareVisemes(text) {
        if (typeof buildVisemeSchedule !== 'function') return;
        const schedule = buildVisemeSchedule(text);
        this.visemeSchedule = schedule.segments;
    }

    setVisemeDuration(d) {
        this.visemeDuration = Number.isFinite(d) && d >= 0 ? d : 0;
    }

    setSpeaking(s) {
        this.speaking = !!s;
        if (s) {
            this.speechClock = 0;
            this.lipRelease.remainingMs = 0;
        }
    }

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

    _updateLipSync(delta) {
        if (!this.vrm?.expressionManager) return;
        const em = this.vrm.expressionManager;
        const active = this.speaking && this.visemeDuration > 0;

        if (!active) {
            if (this.lipRelease.remainingMs > 0) {
                this.lipRelease.remainingMs = Math.max(0, this.lipRelease.remainingMs - delta * 1000);
                const blend = this._smoothstep(1 - this.lipRelease.remainingMs / LIP_RELEASE_DURATION_MS);
                const faded = this.lipRelease.lastForcedValue * (1 - blend);
                for (const bs of Object.values(VOWEL_MAP)) {
                    em.setValue(bs, faded);
                }
                for (const key of Object.keys(this.smoothedVowels)) {
                    this.smoothedVowels[key] = faded;
                }
            } else {
                for (const bs of Object.values(VOWEL_MAP)) {
                    this.smoothedVowels[bs] = 0;
                    em.setValue(bs, 0);
                }
                this.lipEnv = 0;
            }
            return;
        }

        this.lipRelease.remainingMs = LIP_RELEASE_DURATION_MS;
        this.speechClock += delta;

        // Amplitude from the live waveform when the analyser runs, otherwise
        // from the viseme schedule so the mouth still moves.
        const rms = this._computeRMS();
        const amp = Math.pow(Math.min(rms * 1.2, 1), 0.55);
        const envRate = amp > this.lipEnv ? LIP_ATTACK : LIP_RELEASE;
        this.lipEnv += (amp - this.lipEnv) * (1 - Math.exp(-envRate * delta));

        const playhead = this._playhead();
        // The phase wraps: when the audio outlasts the reported duration the
        // mouth must keep moving instead of freezing on the last viseme.
        const phase = this.visemeDuration > 0 ? (playhead / this.visemeDuration) % 1 : 0;
        const seg = (typeof visemeSegmentAt === 'function' && this.visemeSchedule)
            ? visemeSegmentAt(this.visemeSchedule, phase)
            : null;

        let openness = this.lipEnv * 1.2;
        let vowel = null;
        if (seg) {
            vowel = seg.viseme;
            if (openness <= 0.01) openness = seg.open * 0.5;
        } else {
            // No schedule (no reply text seen): alternate vowels on a timer so
            // the mouth still moves while she talks.
            this.fallbackVowel = (this.fallbackVowel + delta * 5) % VOWEL_KEYS.length;
            vowel = VOWEL_MAP[VOWEL_KEYS[Math.floor(this.fallbackVowel)]];
            openness = Math.max(openness, 0.25);
        }
        openness = clamp(openness, 0, LIP_CAP);

        const target = { A: 0, E: 0, I: 0, O: 0, U: 0 };
        const key = vowel ? Object.keys(VOWEL_MAP).find((k) => VOWEL_MAP[k] === vowel) : null;
        if (key) {
            target[key] = openness;
        } else {
            // A consonant or a pause: a small neutral opening, so the mouth
            // does not snap shut between every syllable.
            target.I = openness * 0.4;
        }

        let maxWeight = 0;
        for (const key of VOWEL_KEYS) {
            const bs = VOWEL_MAP[key];
            const from = this.smoothedVowels[bs];
            const to = target[key];
            const rate = 1 - Math.exp(-(to > from ? LIP_ATTACK : LIP_RELEASE) * delta);
            this.smoothedVowels[bs] = from + (to - from) * rate;
            const weight = (this.smoothedVowels[bs] <= 0.01 ? 0 : this.smoothedVowels[bs]) * 0.7;
            em.setValue(bs, weight);
            if (weight > maxWeight) maxWeight = weight;
        }
        this.lipRelease.lastForcedValue = maxWeight;
    }

    _playhead() {
        if (this.audio && typeof this.audio.getPlayhead === 'function') {
            const ctx = this.audio.audioContext;
            if (ctx && ctx.state === 'running') {
                return this.audio.getPlayhead();
            }
        }
        return this.speechClock;
    }

    _smoothstep(t) {
        return t * t * (3 - 2 * t);
    }

    // ── Audio / websocket ───────────────────────────────────────────────────
    async _initWebSocket() {
        // The overlay reuses the companion server's own client and audio
        // helpers; both are optional so the page still works standalone.
        await loadGlobalScript('/js/ws-client.js');
        await loadGlobalScript('/js/audio.js');
        await loadGlobalScript('/js/utils.js');

        if (typeof WSClient === 'undefined') {
            console.warn('[VIRTUAL] WSClient unavailable, running offline');
            return;
        }
        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        this.ws = new WSClient(`${proto}//${location.host}/ws`);

        this.ws.on('emotion.detected', (params) => {
            const emotion = params?.emotion || 'calm';
            const intensity = clamp(Number(params?.intensity) || 0.35, 0.2, 0.6);
            this.setEmotion(emotion, intensity);
        });

        this.ws.on('emotion.mai', (params) => {
            const emotion = params?.emotion || 'neutral';
            const intensity = clamp(Number(params?.intensity) || 0.4, 0.2, 0.7);
            this.setEmotion(emotion, intensity);
        });

        this.ws.on('status.changed', (params) => {
            this.setStatus(params?.status || 'idle');
        });

        // The reply text is what the viseme schedule is built from.
        this.ws.on('chat.response', (params) => {
            if (params?.text) this.prepareVisemes(params.text);
        });

        this.ws.on('tts.chunk', (params) => {
            if (!params?.audio && !params?.done) return;
            this.setVisemeDuration(Number(params?.duration_seconds) || 0);
            this._queueAudio(params);
        });

        this.ws.on('companion.dance', () => this.playAction('dance'));
        this.ws.on('companion.action', (params) => {
            if (params?.action) this.playAction(params.action);
        });
        this.ws.on('companion.automation', (params) => this.handleAutomationPhase(params));

        this.ws.connect();
    }

    _queueAudio(params) {
        const chunk = {
            audio: params.audio || '',
            sample_rate: params.sample_rate,
            done: !!params.done,
            duration: Number(params.duration_seconds) || 0,
        };
        // The audio player is created on demand: the overlay may never be
        // clicked (it can be click-through), and the mouth still has to move.
        if (!this.audio) this._initAudio();
        if (!this.audio) {
            this.pendingAudio.push(chunk);
            return;
        }
        this.audio.queueChunk(chunk.audio, chunk.sample_rate, chunk.done, true, chunk.duration);
    }

    _initAudio() {
        if (this.audio || typeof AudioPlayer === 'undefined') return;
        this.audio = new AudioPlayer();
        this.audio.init();
        this.setAnalyser(this.audio.analyser);
        this.audio.onSpeakingStart = () => this.setSpeaking(true);
        this.audio.onSpeakingEnd = () => this.setSpeaking(false);
        this.audio.onChunkScheduled = (duration) => this.setVisemeDuration(duration);
        for (const chunk of this.pendingAudio) {
            this.audio.queueChunk(chunk.audio, chunk.sample_rate, chunk.done, true, chunk.duration);
        }
        this.pendingAudio.length = 0;
    }

    // ── Input ───────────────────────────────────────────────────────────────
    _initInput() {
        const canvas = this.renderer.domElement;

        canvas.addEventListener('pointerdown', (e) => {
            this._initAudio();
            if (this.audio) this.audio.resume();

            this.dragging = true;
            // Screen-relative coordinates are the fallback when there is no
            // native host (for example when the page is opened in a browser).
            // clientX/clientY move with the window and feed each move back
            // into the next delta.
            this.dragLast.x = e.screenX;
            this.dragLast.y = e.screenY;
            document.body.classList.add('dragging');
            try { canvas.setPointerCapture(e.pointerId); } catch (err) { /* not capturable */ }
            // The host takes over the drag and moves the window directly from
            // WM_MOUSEMOVE. Hand the pointer capture over to it so the page
            // does not need a round-trip for every move.
            this.hostDragging = hostSend('beginDrag');
            if (this.hostDragging) {
                try { canvas.releasePointerCapture(e.pointerId); } catch (err) { /* already released */ }
            }
            // Being picked up: the carried pose replaces the walk cycle.
            this.hostWalking = false;
            this.walkDir = null;
            this.setLocomotion('carried');
            e.preventDefault();
        });

        canvas.addEventListener('pointermove', (e) => {
            if (!this.dragging || this.hostDragging) return;
            const dx = e.screenX - this.dragLast.x;
            const dy = e.screenY - this.dragLast.y;
            this.dragLast.x = e.screenX;
            this.dragLast.y = e.screenY;
            if (dx || dy) hostSend('moveBy', { dx, dy });
        });

        const endDrag = (e) => {
            if (!this.dragging) return;
            const wasHost = this.hostDragging;
            this.dragging = false;
            this.hostDragging = false;
            this.hostWalking = false;
            this.walkDir = null;
            document.body.classList.remove('dragging');
            if (e && e.pointerId !== undefined) {
                try { canvas.releasePointerCapture(e.pointerId); } catch (err) { /* already released */ }
            }
            if (wasHost) hostSend('endDrag');
            this.setLocomotion('idle', { auto: true });
        };
        canvas.addEventListener('pointerup', endDrag);
        canvas.addEventListener('pointercancel', endDrag);
        canvas.addEventListener('dblclick', () => hostSend('perchOnForeground'));

        window.addEventListener('keydown', (e) => {
            if (!e.ctrlKey || !e.altKey) return;
            const key = e.key.toLowerCase();
            if (key === 'c') {
                e.preventDefault();
                this.clickThrough = !this.clickThrough;
                hostSend('setClickThrough', { on: this.clickThrough });
            } else if (key === 'p') {
                e.preventDefault();
                hostSend('perchOnForeground');
            } else if (key === 'q') {
                e.preventDefault();
                hostSend('quit');
            }
        });
    }

    // ── Host events ─────────────────────────────────────────────────────────
    handleAutomationPhase(params) {
        const phase = String(params?.phase || '');
        const duration = Number(params?.duration_ms) || 0;
        switch (phase) {
            case 'walk':
                if (params?.target === 'taskbar') {
                    hostSend('walkToTaskbar', { duration_ms: duration || 1600 });
                } else {
                    hostSend('walkToForeground', { duration_ms: duration || 1800 });
                }
                break;
            case 'click':
                this.playGesture('click');
                break;
            case 'type':
            case 'typing':
                this.playGesture('typing');
                break;
            case 'not_found':
                this.playGesture('not_found');
                break;
            case 'done':
                this.playGesture('success');
                break;
            case 'send':
                this.setEmotion('happy', 0.45);
                break;
            case 'await_confirm':
                this.setEmotion('think', 0.5);
                break;
            case 'cancelled':
                this.setEmotion('relaxed', 0.5);
                break;
            default:
                console.log('[VIRTUAL] automation phase:', phase, params);
        }
    }

    handleHostEvent(evt) {
        if (!evt) return;
        switch (evt.event) {
            case 'perched':
                console.log('[VIRTUAL] perched on foreground window');
                break;
            case 'moved':
                this._onHostMoved(evt);
                break;
            case 'dragEnd':
                this.dragging = false;
                this.hostDragging = false;
                this.hostWalking = false;
                this.walkDir = null;
                document.body.classList.remove('dragging');
                this.setLocomotion('idle', { auto: true });
                break;
            case 'walkStart':
                this._onWalkStart(evt);
                break;
            case 'walkEnd':
                this._onWalkEnd(evt);
                break;
            case 'clickThrough':
                this.clickThrough = !!evt.on;
                console.log('[VIRTUAL] click-through:', this.clickThrough);
                break;
            default:
                console.log('[VIRTUAL] host event:', evt);
        }
    }

    _onWalkStart(evt) {
        if (this.dragging) {
            hostSend('cancelWalk');
            return;
        }
        const dx = Number(evt?.dx) || 0;
        const dy = Number(evt?.dy) || 0;
        this.hostWalking = true;
        this.walkDir = { dx, dy };
        this._selectWalkClip(dx, dy);
        clearTimeout(this.moveIdleTimer);
        this.setLocomotion('walk', { auto: true });
    }

    _onWalkEnd(evt) {
        if (!this.hostWalking) return;
        this.hostWalking = false;
        this.walkDir = null;
        if (this.dragging) return;
        this.setLocomotion('idle', { auto: true });
    }

    _selectWalkClip(dx, dy) {
        const key = selectWalkClipKey(dx, dy);
        if (this.layer?.has(key)) {
            this.walkClipKey = key;
        } else if (this.layer?.has('walk-front')) {
            this.walkClipKey = 'walk-front';
        } else {
            this.walkClipKey = null;
        }
        return this.walkClipKey;
    }

    // The host reports every window move; fast moves mean she is being
    // hurried across the desktop, slow ones a stroll.
    _onHostMoved(evt) {
        const now = performance.now();
        const dt = this.lastMoveEventAt ? (now - this.lastMoveEventAt) / 1000 : 0.5;
        this.lastMoveEventAt = now;

        let speed = 0;
        if (typeof evt.dx === 'number' && typeof evt.dy === 'number') {
            speed = Math.hypot(evt.dx, evt.dy) / Math.max(dt, 0.016);
        } else {
            // No distance: use the event rate as a proxy (moves per second).
            speed = dt > 0 ? 1 / dt : 0;
        }

        if (this.locomotion === 'carried') return;
        // Scripted host walks already selected a directional clip in
        // _onWalkStart. Do not let the generic drag-speed heuristic replace it.
        if (this.hostWalking) return;
        if (speed > 900) this.setLocomotion('run', { auto: true });
        else if (speed > 120) this.setLocomotion('walk', { auto: true });
        else this.setLocomotion('walk', { auto: true });

        clearTimeout(this.moveIdleTimer);
        this.moveIdleTimer = setTimeout(() => {
            this.lastMoveEventAt = 0;
            if (!this.dragging) this.setLocomotion('idle', { auto: true });
        }, 600);
    }

    // ── Frame loop ──────────────────────────────────────────────────────────
    _animate() {
        requestAnimationFrame(() => this._animate());
        if (!this.renderer) return;

        const now = performance.now();
        const delta = Math.min((now - this.clock.last) / 1000, 0.1);
        this.clock.last = now;

        if (this.vrm) {
            // The head/neck status offset is render-only. Put the un-offset
            // base back before the mixer blends, otherwise a clip that is
            // fading in/out would blend from the overlaid pose and the offset
            // would briefly stack.
            this._restoreHeadBases();
            if (this.mixer) this.mixer.update(delta);

            // Walk clips write head/neck in the retargeted skeleton; put the
            // head back upright before the status overlay is composed on top.
            const stabilized = this.vrmaLocomotion && !!this._stabilizeWalkHead();

            if (this.rig) {
                this.rig.syncFromMixer();
                if (this.vrmaLocomotion) {
                    // The walk VRMA owns the body; _stabilizeWalkHead already
                    // replaced the head/neck pose above.
                } else if (this.locomotion !== 'idle') {
                    this.rig.update(delta);
                } else {
                    // Idle: the mixer owns the bones again. Bones the clip
                    // does not animate go back to the rest pose.
                    this.rig.restoreRestPose();
                }
            }

            this._captureHeadBases(stabilized);
            this._updateStatusPose(delta);
            this._applyStatusPose();
            this._applyArmGesture();

            this._updateBlink(delta);
            this._updateExpressions(delta);
            this._updateLipSync(delta);

            // One update call covers the humanoid, the look-at, the expression
            // manager and the spring bones.
            this.vrm.update(delta);
        }

        this.renderer.render(this.scene, this.camera);
        if (!this.loggedRender && this.vrm) {
            this.loggedRender = true;
            const info = this.renderer.info.render;
            const box = new THREE.Box3().setFromObject(this.vrm.scene);
            const frustum = new THREE.Frustum().setFromProjectionMatrix(
                new THREE.Matrix4().multiplyMatrices(
                    this.camera.projectionMatrix, this.camera.matrixWorldInverse));
            console.log('[VIRTUAL] render', JSON.stringify({
                calls: info.calls,
                triangles: info.triangles,
                frustumCullsModel: !frustum.intersectsBox(box),
                modelBox: {
                    min: box.min.toArray().map((v) => +v.toFixed(3)),
                    max: box.max.toArray().map((v) => +v.toFixed(3)),
                },
            }));
        }
    }

    _stabilizeWalkHead() {
        if (!this.vrm?.humanoid) return false;
        const h = this.vrm.humanoid;
        const neck = h.getNormalizedBoneNode('neck');
        const head = h.getNormalizedBoneNode('head');
        if (neck && this.neckRest) neck.quaternion.copy(this.neckRest);
        if (head && this.headRest) head.quaternion.copy(this.headRest);

        // A tiny walk bob keeps the head from looking frozen while the body
        // moves. It is deliberately small; the old failure was a large fold.
        const t = performance.now() / 1000;
        if (head) {
            head.rotation.x += Math.sin(t * 5.6) * 0.012;
            head.rotation.z += Math.sin(t * 2.8) * 0.008;
        }
        return true;
    }

    // Undo the previous frame's status offset on the bones the overlay wrote.
    _restoreHeadBases() {
        if (!this.vrm?.humanoid) return;
        const h = this.vrm.humanoid;
        const head = h.getNormalizedBoneNode('head');
        const neck = h.getNormalizedBoneNode('neck');
        if (head) head.quaternion.copy(this._headBase);
        if (neck) neck.quaternion.copy(this._neckBase);
    }

    // Snapshot the pose the mixer/rest owns before the status overlay runs.
    // The idle clip animates the neck but not the head (and the procedural
    // layer animates neither), so a bone that is not driven has to fall back
    // to rest; otherwise each frame would add the offset to the last one.
    _captureHeadBases(stabilized) {
        if (!this.vrm?.humanoid) return;
        const h = this.vrm.humanoid;
        const driven = this.layer?.drivenNodes?.() || null;
        const capture = (node, rest, base) => {
            if (!node) return;
            if (stabilized || (driven && driven.has(node.name))) {
                base.copy(node.quaternion);
            } else {
                base.copy(rest || node.quaternion);
            }
        };
        capture(h.getNormalizedBoneNode('head'), this.headRest, this._headBase);
        capture(h.getNormalizedBoneNode('neck'), this.neckRest, this._neckBase);
    }

    // The status pose is a small head/neck offset on top of the base captured
    // above. Quaternion composition (base * delta) replaces the old Euler
    // `+=`, which grew without bound whenever no clip rewrote the bone.
    _applyStatusPose() {
        if (!this.vrm?.humanoid) return;
        const h = this.vrm.humanoid;
        const head = h.getNormalizedBoneNode('head');
        const neck = h.getNormalizedBoneNode('neck');
        const pitch = this.statusPose.pitch + this.gesturePose.pitch;
        const yaw = this.statusPose.yaw + this.gesturePose.yaw;
        const roll = this.statusPose.roll + this.gesturePose.roll;

        this._statusEuler.set(pitch, yaw, roll, 'XYZ');
        this._statusDelta.setFromEuler(this._statusEuler);
        this._statusEuler.set(pitch * 0.4, yaw * 0.4, 0, 'XYZ');
        this._neckDelta.setFromEuler(this._statusEuler);

        if (head) head.quaternion.copy(this._headBase).multiply(this._statusDelta);
        if (neck) neck.quaternion.copy(this._neckBase).multiply(this._neckDelta);
    }

    // Click/typing arm overlay. Only run while the mixer owns the arms (idle or
    // VRMA walk); procedural locomotion already writes the upper arms and would
    // otherwise accumulate the offsets.
    _applyArmGesture() {
        if (!this.gestureName || !this.vrm?.humanoid) return;
        if (this.locomotion !== 'idle' && !this.vrmaLocomotion) return;

        const elapsed = (performance.now() - this.gestureStart) / 1000;
        const duration = this.gestureDuration / 1000;
        if (duration <= 0 || elapsed >= duration) return;
        const progress = Math.max(0, Math.min(1, elapsed / duration));
        const h = this.vrm.humanoid;

        if (this.gestureName === 'click') {
            const pulse = Math.sin(Math.PI * progress);
            const upper = h.getNormalizedBoneNode('rightUpperArm');
            const lower = h.getNormalizedBoneNode('rightLowerArm');
            const hand = h.getNormalizedBoneNode('rightHand');
            if (upper) {
                upper.rotation.x -= 0.55 * pulse;
                upper.rotation.z += 0.12 * pulse;
            }
            if (lower) lower.rotation.x -= 0.45 * pulse;
            if (hand) hand.rotation.x -= 0.18 * pulse;
        } else if (this.gestureName === 'typing') {
            const wiggle = Math.sin(elapsed * 18) * 0.08;
            for (const side of ['left', 'right']) {
                const sign = side === 'left' ? 1 : -1;
                const upper = h.getNormalizedBoneNode(`${side}UpperArm`);
                const lower = h.getNormalizedBoneNode(`${side}LowerArm`);
                const hand = h.getNormalizedBoneNode(`${side}Hand`);
                if (upper) {
                    upper.rotation.x -= 0.32;
                    upper.rotation.z += 0.10 * sign;
                }
                if (lower) lower.rotation.x -= 0.5 + wiggle * sign;
                if (hand) hand.rotation.x -= 0.15;
            }
        }
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// Small helpers
// ─────────────────────────────────────────────────────────────────────────────
// The shared client/audio helpers live in the main app's static tree; load
// them from their canonical path and degrade quietly if they are missing.
function loadGlobalScript(url) {
    return new Promise((resolve) => {
        if (document.querySelector(`script[data-src="${url}"]`)) {
            resolve(false);
            return;
        }
        const script = document.createElement('script');
        script.src = url;
        script.async = false;
        script.dataset.src = url;
        script.onload = () => resolve(true);
        script.onerror = () => {
            console.warn('[VIRTUAL] optional script unavailable:', url);
            resolve(false);
        };
        document.head.appendChild(script);
    });
}

// ─────────────────────────────────────────────────────────────────────────────
// Boot
// ─────────────────────────────────────────────────────────────────────────────
const overlay = new VirtualOverlay(document.getElementById('stage'));
overlay.init();
overlay._loadModel();

// The host calls window.virtual.onHostEvent(json) with a JSON string.
window.virtual = {
    onHostEvent(json) {
        let evt = json;
        if (typeof json === 'string') {
            try {
                evt = JSON.parse(json);
            } catch (e) {
                console.warn('[VIRTUAL] malformed host event:', json);
                return;
            }
        }
        dispatchHostEvent(evt);
    },
    // Convenience API for the host / for manual testing in a browser.
    perch: () => hostSend('perchOnForeground'),
    setClickThrough: (on) => {
        overlay.clickThrough = !!on;
        return hostSend('setClickThrough', { on: !!on });
    },
    quit: () => hostSend('quit'),
    setLocomotion: (mode) => overlay.setLocomotion(mode),
    playAction: (name) => overlay.playAction(name),
    overlay,
};

// Once the scene is ready the queued host events are delivered.
const readyCheck = setInterval(() => {
    if (!overlay.loaded) return;
    clearInterval(readyCheck);
    hostEventTarget = (evt) => overlay.handleHostEvent(evt);
    while (pendingHostEvents.length) {
        hostEventTarget(pendingHostEvents.shift());
    }
}, 100);
