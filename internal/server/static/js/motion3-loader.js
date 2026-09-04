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
                this.curves.push({ id: curve.Id, segments: this._parseSegments(curve.Segments) });
            }
        }
    }
    _parseSegments(raw) {
        const keys = [];
        let i = 0;
        while (i < raw.length) {
            const time = raw[i], value = raw[i + 1], interp = raw[i + 2];
            if (interp === 1) {
                keys.push({ time, value, interp: 'bezier', cx1: raw[i+3], cy1: raw[i+4], cx2: raw[i+5], cy2: raw[i+6] });
                i += 7;
            } else {
                keys.push({ time, value, interp: 'linear' });
                i += 3;
            }
        }
        return keys;
    }
    evaluate(paramId, time) {
        const curve = this.curves.find(c => c.id === paramId);
        if (!curve || curve.segments.length === 0) return null;
        const segs = curve.segments;
        const t = this.loop ? (time % this.duration) : Math.min(time, this.duration);
        let prev = segs[0], next = segs[segs.length - 1];
        for (let i = 0; i < segs.length - 1; i++) {
            if (t >= segs[i].time && t <= segs[i + 1].time) { prev = segs[i]; next = segs[i + 1]; break; }
        }
        if (prev === next) return prev.value;
        const localT = (t - prev.time) / (next.time - prev.time || 1);
        const eased = localT * localT * (3 - 2 * localT);
        return prev.value + (next.value - prev.value) * eased;
    }
}

async function loadMotion3(url) {
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`Failed to load motion: ${url}`);
    return new Motion3Clip(await resp.json(), url.split('/').pop().replace('.motion3.json', ''));
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
    '/dance/dance_show.motion3.json',
    '/motions/smile.motion3.json',
    '/motions/happy.motion3.json',
    '/motions/sad.motion3.json',
    '/motions/flustered.motion3.json',
    '/motions/pouting.motion3.json',
    '/motions/crying.motion3.json',
    '/motions/depression.motion3.json',
    '/motions/angry.motion3.json',
    '/motions/surprised.motion3.json',
    '/motions/thinking.motion3.json',
    '/motions/wave.motion3.json',
    '/motions/nod.motion3.json',
    '/motions/headshake.motion3.json',
];

const PARAM_MAP = {
    ParamAngleX: 'ParamAngleX', ParamAngleY: 'ParamAngleY', ParamAngleZ: 'ParamAngleZ',
    ParamCheek: 'ParamCheek', ParamEyeLOpen: 'ParamEyeLOpen', ParamEyeLSmile: 'ParamEyeLSmile',
    ParamEyeROpen: 'ParamEyeROpen', ParamEyeRSmile: 'ParamEyeRSmile',
    ParamEyeBallX: 'ParamEyeBallX', ParamEyeBallY: 'ParamEyeBallY',
    ParamBrowLForm: 'ParamBrowLForm', ParamBrowRForm: 'ParamBrowRForm',
    ParamMouthForm: 'ParamMouthForm', ParamMouthOpenY: 'ParamMouthOpenY',
    ParamBodyAngleX: 'ParamBodyAngleX', ParamBodyAngleY: 'ParamBodyAngleY', ParamBodyAngleZ: 'ParamBodyAngleZ',
    ParamBreath: 'ParamBreath', ParamArmLA: 'ParamArmLA', ParamArmRA: 'ParamArmRA',
    ParamHairAhoge: 'ParamHairAhoge',
};

function getMotionByName(clips, name) {
    if (!Array.isArray(clips) || !name) return null;
    const target = name.toLowerCase().trim();
    return clips.find(c => c && c.name && c.name.toLowerCase() === target) || null;
}

window.Motion3Clip = Motion3Clip;
window.loadMotion3 = loadMotion3;
window.MOTION_FILES = MOTION_FILES;
window.PARAM_MAP = PARAM_MAP;
window.getMotionByName = getMotionByName;

