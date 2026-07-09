// Motion3.json Loader — parses VRoid .motion3.json files into playable animations.
// Converts parameter curves (ParamAngleX, ParamMouthOpenY, etc.) into per-frame values.

class Motion3Clip {
    constructor(json, name) {
        this.name = name || json.Meta?.Name || 'unnamed';
        this.duration = json.Meta?.Duration || 0;
        this.fps = json.Meta?.Fps || 30;
        this.loop = json.Meta?.Loop !== false;
        this.curves = [];
        this.partOpacities = [];

        for (const curve of json.Curves || []) {
            if (curve.Target === 'Parameter') {
                this.curves.push({
                    id: curve.Id,
                    segments: this._parseSegments(curve.Segments),
                });
            } else if (curve.Target === 'Part' && curve.Id) {
                // Part opacity curves (e.g., PartArmA)
                this.partOpacities.push({
                    id: curve.Id,
                    segments: this._parseSegments(curve.Segments),
                });
            }
        }
    }

    // Parse flat segment array into keyframe objects
    // Format: [time, value, 'bezier', cx1, cy1, cx2, cy2, time, value, ...]
    _parseSegments(raw) {
        const keys = [];
        let i = 0;
        while (i < raw.length) {
            const time = raw[i];
            const value = raw[i + 1];
            const interp = raw[i + 2];
            if (interp === 1) {
                // Bezier curve: [time, value, 1, cx1, cy1, cx2, cy2]
                keys.push({
                    time, value,
                    interp: 'bezier',
                    cx1: raw[i + 3], cy1: raw[i + 4],
                    cx2: raw[i + 5], cy2: raw[i + 6],
                });
                i += 7;
            } else {
                // Linear: [time, value, 0]
                keys.push({ time, value, interp: 'linear' });
                i += 3;
            }
        }
        return keys;
    }

    // Evaluate curve at given time
    evaluate(paramId, time) {
        const curve = this.curves.find(c => c.id === paramId);
        if (!curve || curve.segments.length === 0) return null;

        const segs = curve.segments;
        const t = this.loop ? (time % this.duration) : Math.min(time, this.duration);

        // Find surrounding keyframes
        let prev = segs[0];
        let next = segs[segs.length - 1];
        for (let i = 0; i < segs.length - 1; i++) {
            if (t >= segs[i].time && t <= segs[i + 1].time) {
                prev = segs[i];
                next = segs[i + 1];
                break;
            }
        }

        if (prev === next) return prev.value;

        const localT = (t - prev.time) / (next.time - prev.time || 1);

        if (prev.interp === 'bezier' && next.interp === 'bezier') {
            // Cubic bezier interpolation
            return this._bezierInterpolate(prev.value, next.value, localT, prev.cy1, prev.cy2, next.cy1, next.cy2);
        }

        // Linear
        return prev.value + (next.value - prev.value) * localT;
    }

    _bezierInterpolate(from, to, t, _cy1, _cy2, _cy1b, _cy2b) {
        // Simplified cubic bezier (approximate with control points)
        const eased = t * t * (3 - 2 * t); // smoothstep fallback
        return from + (to - from) * eased;
    }
}

// Load a motion3.json file and return a Motion3Clip
async function loadMotion3(url) {
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`Failed to load motion: ${url}`);
    const json = await resp.json();
    return new Motion3Clip(json, url.split('/').pop().replace('.motion3.json', ''));
}

// Available motion clips — loaded after model init
const MOTION_FILES = [
    '/motion/standby5_animation.json',
];

// Motion parameter → VRM coreModel parameter ID mapping
const PARAM_MAP = {
    ParamAngleX: 'ParamAngleX',
    ParamAngleY: 'ParamAngleY',
    ParamAngleZ: 'ParamAngleZ',
    ParamCheek: 'ParamCheek',
    ParamEyeLOpen: 'ParamEyeLOpen',
    ParamEyeLSmile: 'ParamEyeLSmile',
    ParamEyeROpen: 'ParamEyeROpen',
    ParamEyeRSmile: 'ParamEyeRSmile',
    ParamEyeBallX: 'ParamEyeBallX',
    ParamEyeBallY: 'ParamEyeBallY',
    ParamBrowLForm: 'ParamBrowLForm',
    ParamBrowRForm: 'ParamBrowRForm',
    ParamMouthForm: 'ParamMouthForm',
    ParamMouthOpenY: 'ParamMouthOpenY',
    ParamBodyAngleX: 'ParamBodyAngleX',
    ParamBodyAngleY: 'ParamBodyAngleY',
    ParamBodyAngleZ: 'ParamBodyAngleZ',
    ParamBreath: 'ParamBreath',
    ParamArmLA: 'ParamArmLA',
    ParamArmRA: 'ParamArmRA',
    ParamHairAhoge: 'ParamHairAhoge',
};

// Custom bone animation loader for standby5_animation.json format
class BoneAnimationClip {
    constructor(json, name) {
        this.name = name || json.name || 'unnamed';
        this.duration = json.duration || 4.0;
        this.tracks = json.tracks || [];
    }

    // Get bone transform at given time
    getBoneTransform(boneName, time) {
        const track = this.tracks.find(t => t.name === boneName);
        if (!track) return null;

        const t = time % this.duration;
        const times = track.times;
        const values = track.values;

        // Find surrounding keyframes
        let idx = 0;
        while (idx < times.length - 1 && times[idx + 1] < t) {
            idx++;
        }

        if (idx >= times.length - 1) {
            // Return last value
            const offset = (times.length - 1) * (track.type === 'vector' ? 3 : 4);
            if (track.type === 'vector') {
                return new THREE.Vector3(values[offset], values[offset + 1], values[offset + 2]);
            } else {
                return new THREE.Quaternion(values[offset], values[offset + 1], values[offset + 2], values[offset + 3]);
            }
        }

        const t0 = times[idx];
        const t1 = times[idx + 1];
        const alpha = (t - t0) / (t1 - t0);

        if (track.type === 'vector') {
            const offset0 = idx * 3;
            const offset1 = (idx + 1) * 3;
            return new THREE.Vector3(
                lerp(values[offset0], values[offset1], alpha),
                lerp(values[offset0 + 1], values[offset1 + 1], alpha),
                lerp(values[offset0 + 2], values[offset1 + 2], alpha)
            );
        } else {
            const offset0 = idx * 4;
            const offset1 = (idx + 1) * 4;
            return new THREE.Quaternion().fromArray([
                lerp(values[offset0], values[offset1], alpha),
                lerp(values[offset0 + 1], values[offset1 + 1], alpha),
                lerp(values[offset0 + 2], values[offset1 + 2], alpha),
                lerp(values[offset0 + 3], values[offset1 + 3], alpha)
            ]).normalize();
        }
    }
}

function lerp(a, b, t) {
    return a + (b - a) * t;
}

async function loadBoneAnimation(url) {
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`Failed to load bone animation: ${url}`);
    const json = await resp.json();
    return new BoneAnimationClip(json, url.split('/').pop().replace('.json', ''));
}

// Load facial expressions JSON
async function loadFacialExpressions(url) {
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`Failed to load facial expressions: ${url}`);
    const json = await resp.json();
    return json.expressions || {};
}

window.Motion3Clip = Motion3Clip;
window.loadMotion3 = loadMotion3;
window.MOTION_FILES = MOTION_FILES;
window.PARAM_MAP = PARAM_MAP;
window.BoneAnimationClip = BoneAnimationClip;
window.loadBoneAnimation = loadBoneAnimation;
window.loadFacialExpressions = loadFacialExpressions;
