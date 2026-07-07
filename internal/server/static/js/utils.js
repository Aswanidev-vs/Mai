// Utility functions

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

function renderMarkdown(text) {
    let html = escapeHtml(text);
    // Code blocks
    html = html.replace(/```(\w*)\n([\s\S]*?)```/g, '<pre><code>$2</code></pre>');
    // Inline code
    html = html.replace(/`([^`]+)`/g, '<code>$1</code>');
    // Bold
    html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
    // Italic
    html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');
    // Line breaks
    html = html.replace(/\n/g, '<br>');
    return html;
}

function debounce(fn, ms) {
    let timer;
    return function (...args) {
        clearTimeout(timer);
        timer = setTimeout(() => fn.apply(this, args), ms);
    };
}

function generateId() {
    if (crypto?.randomUUID) return crypto.randomUUID();
    return Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
}

// ── Viseme schedule for accurate lip sync ──
// Maps spoken text to a normalized (0..1) timeline of mouth shapes so the
// character's lip movements follow the actual words rather than raw audio volume.

// Latin vowel -> VRM mouth viseme channel
const VISEME_VOWELS = {
    a: 'aa', e: 'ee', i: 'ih', o: 'oh', u: 'ou',
    à: 'aa', á: 'aa', â: 'aa', ã: 'aa', ä: 'aa', æ: 'aa', å: 'aa',
    è: 'ee', é: 'ee', ê: 'ee', ë: 'ee',
    ì: 'ih', í: 'ih', î: 'ih', ï: 'ih',
    ò: 'oh', ó: 'oh', ô: 'oh', õ: 'oh', ö: 'oh',
    ù: 'ou', ú: 'ou', û: 'ou', ü: 'ou',
};

const isLatinVowel = (ch) => Object.prototype.hasOwnProperty.call(VISEME_VOWELS, ch.toLowerCase());
const isCJK = (ch) => /[一-鿿]/.test(ch);
const isDigit = (ch) => ch >= '0' && ch <= '9';
const isWhitespace = (ch) => /\s/.test(ch);
const isPunctuation = (ch) => /[.,!?;:'"()\-—…、。！？；：""''（）]/.test(ch);

// Returns { segments: [{ t0, t1, viseme, open }] } normalized across 0..1.
function buildVisemeSchedule(text) {
    if (!text) return { segments: [] };

    const units = [];
    for (const raw of text) {
        const ch = raw.toLowerCase();
        if (isWhitespace(ch) || isPunctuation(ch)) {
            units.push({ viseme: null, weight: 0.35, open: 0.0 }); // pause / closure
        } else if (isLatinVowel(ch)) {
            units.push({ viseme: VISEME_VOWELS[ch] || 'aa', weight: 1.6, open: 1.0 });
        } else if (isCJK(raw)) {
            units.push({ viseme: 'aa', weight: 1.4, open: 1.0 }); // open syllable
        } else if (isDigit(ch)) {
            units.push({ viseme: 'oh', weight: 1.2, open: 0.8 });
        } else {
            units.push({ viseme: null, weight: 1.0, open: 0.12 }); // consonant: near-closed
        }
    }

    const totalWeight = units.reduce((s, u) => s + u.weight, 0) || 1;
    const segments = [];
    let cursor = 0;
    for (const u of units) {
        const dur = u.weight / totalWeight;
        const t0 = cursor;
        const t1 = Math.min(1, cursor + dur);
        segments.push({ t0, t1, viseme: u.viseme, open: u.open });
        cursor = t1;
    }
    // Guard against floating-point overshoot
    if (segments.length > 0) segments[segments.length - 1].t1 = 1.0;

    return { segments };
}

// Find the segment active at normalized phase [0..1].
function visemeSegmentAt(segments, phase) {
    if (!segments || segments.length === 0) return null;
    const p = Math.max(0, Math.min(1, phase));
    for (const s of segments) {
        if (p >= s.t0 && p < s.t1) return s;
    }
    return segments[segments.length - 1];
}

// Smoothstep-style gate: 0 below lo, 1 above hi, smooth between.
function energyGate(value, lo, hi) {
    if (value <= lo) return 0;
    if (value >= hi) return 1;
    const t = (value - lo) / (hi - lo);
    return t * t * (3 - 2 * t);
}

// Explicit globals for Codacy cross-file resolution
Object.assign(window, { escapeHtml, renderMarkdown, debounce, generateId, buildVisemeSchedule, visemeSegmentAt, energyGate });
