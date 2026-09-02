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

// ── Prosody-Enhanced Viseme Schedule ──
// Maps spoken text to a normalized (0..1) timeline of mouth shapes with
// natural speech rhythm: punctuation pauses, stress emphasis, breath points.

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
const isSentenceEnd = (ch) => /[.!?。！？]/.test(ch);
const isClauseEnd = (ch) => /[,;:—、；：]/.test(ch);

// Random jitter: ±10% of value for natural rhythm variance
function jitter(val) {
    return val * (0.9 + Math.random() * 0.2);
}

// Returns { segments: [{ t0, t1, viseme, open }] } normalized across 0..1.
// Now with prosody: punctuation pauses, stress emphasis, breath points.
function buildVisemeSchedule(text) {
    if (!text) return { segments: [] };

    // Step 1: Build raw units with weights
    const units = [];
    let prevWasSpace = false;

    for (let i = 0; i < text.length; i++) {
        const raw = text[i];
        const ch = raw.toLowerCase();

        if (isWhitespace(ch)) {
            if (!prevWasSpace) {
                units.push({ viseme: null, weight: jitter(0.4), open: 0.0 });
            }
            prevWasSpace = true;
            continue;
        }
        prevWasSpace = false;

        if (isSentenceEnd(raw)) {
            // Sentence-ending punctuation: longer pause (breath point)
            // Treat an ellipsis as one pause rather than three consecutive
            // breath points, which would make a natural "hmm..." sound oddly
            // stretched during lip-sync.
            if (raw === '.' && i + 1 < text.length && text[i + 1] === '.') {
                while (i + 1 < text.length && text[i + 1] === '.') i++;
            }
            units.push({ viseme: null, weight: jitter(1.8), open: 0.0 });
        } else if (isClauseEnd(raw)) {
            // Clause boundary: medium pause
            units.push({ viseme: null, weight: jitter(0.9), open: 0.0 });
        } else if (isLatinVowel(ch)) {
            // Check if this is a stressed syllable (uppercase or start of word)
            const isStressed = raw !== ch || (i > 0 && (text[i-1] === ' ' || text[i-1] === undefined));
            const baseWeight = isStressed ? 2.0 : 1.5;
            units.push({ viseme: VISEME_VOWELS[ch] || 'aa', weight: jitter(baseWeight), open: 1.0 });
        } else if (isCJK(raw)) {
            units.push({ viseme: 'aa', weight: jitter(1.4), open: 1.0 });
        } else if (isDigit(ch)) {
            units.push({ viseme: 'oh', weight: jitter(1.2), open: 0.8 });
        } else {
            units.push({ viseme: null, weight: jitter(0.9), open: 0.15 });
        }
    }

    // Step 2: Add breath points for long sentences (>12 words)
    const wordCount = text.split(/\s+/).filter(w => w.length > 0).length;
    if (wordCount > 12) {
        // Insert a micro-pause at the natural midpoint
        const midIdx = Math.floor(units.length / 2);
        // Find nearest whitespace/punctuation around midpoint
        for (let offset = 0; offset < 10; offset++) {
            const checkIdx = midIdx + offset;
            if (checkIdx < units.length && units[checkIdx].viseme === null) {
                units[checkIdx].weight *= 1.5; // Extend existing pause
                break;
            }
            const checkIdx2 = midIdx - offset;
            if (checkIdx2 >= 0 && units[checkIdx2].viseme === null) {
                units[checkIdx2].weight *= 1.5;
                break;
            }
        }
    }

    // Step 3: Normalize to 0..1 timeline
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
