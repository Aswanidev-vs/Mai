// Motion3.json Loader — parses VRoid .motion3.json files into playable parameter animations.

class Motion3Clip {
    constructor(json, name) {
        this.name = name || json.Meta?.Name || 'unnamed';
        this.duration = json.Meta?.Duration || 0;
        this.fps = json.Meta?.Fps || 30;
        this.loop = json.Meta?.Loop !== false;
        this.curves = [];

        for (const curve of json.Curves || []) {
            if (curve.Target === 'Parameter') {
                this.curves.push({
                    id: curve.Id,
                    segments: this._parseSegments(curve.Segments),
                });
            }
        }
    }

    // Parse flat segment array: [time, value, interp, ...]
    // interp: 0=linear, 1=bezier(cx1,cy1,cx2,cy2)
    _parseSegments(raw) {
        const keys = [];
        let i = 0;
        while (i < raw.length) {
            const time = raw[i];
            const value = raw[i + 1];
            const interp = raw[i + 2];
            if (interp === 1) {
                keys.push({
                    time, value, interp: 'bezier',
                    cx1: raw[i + 3], cy1: raw[i + 4],
                    cx2: raw[i + 5], cy2: raw[i + 6],
                });
                i += 7;
            } else {
                keys.push({ time, value, interp: 'linear' });
                i += 3;
            }
        }
        return keys;
    }

    // Evaluate a parameter curve at given time
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
        // Smoothstep interpolation
        const eased = localT * localT * (3 - 2 * localT);
        return prev.value + (next.value - prev.value) * eased;
    }
}

// Load a motion3.json file and return a Motion3Clip
async function loadMotion3(url) {
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`Failed to load motion: ${url}`);
    const json = await resp.json();
    return new Motion3Clip(json, url.split('/').pop().replace('.motion3.json', ''));
}

const MOTION_FILES = [
    '/assets/hiyori_m01.motion3.json',
    '/assets/hiyori_m02.motion3.json',
    '/assets/hiyori_m03.motion3.json',
    '/assets/hiyori_m04.motion3.json',
    '/assets/hiyori_m05.motion3.json',
    '/assets/hiyori_m06.motion3.json',
    '/assets/hiyori_m07.motion3.json',
    '/assets/hiyori_m08.motion3.json',
];

// VRM parameter IDs that motion3 files can animate
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

window.Motion3Clip = Motion3Clip;
window.loadMotion3 = loadMotion3;
window.MOTION_FILES = MOTION_FILES;
window.PARAM_MAP = PARAM_MAP;
