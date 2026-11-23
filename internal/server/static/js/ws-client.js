// WebSocket client with auto-reconnect and message routing

// Binary frame layout (see internal/server/protocol.go).
const BINARY_TAG_TTS = 0x54;
const TTS_HEADER_BYTES = 14;

class WSClient {
    constructor(url) {
        this.url = url;
        this.ws = null;
        this.handlers = {};
        this.reconnectDelay = 1000;
        this.maxReconnectDelay = 30000;
        this.connected = false;
        this.onConnect = null;
        this.onDisconnect = null;
    }

    connect() {
        if (this.ws && (this.ws.readyState === WebSocket.CONNECTING || this.ws.readyState === WebSocket.OPEN)) {
            return;
        }

        this.ws = new WebSocket(this.url);
        // Keep binary frames as ArrayBuffers so the PCM view is zero-copy.
        this.ws.binaryType = 'arraybuffer';

        this.ws.onopen = () => {
            this.connected = true;
            this.reconnectDelay = 1000;
            console.log('[WS] Connected');
            if (this.onConnect) this.onConnect();
        };

        this.ws.onmessage = (event) => {
            // Text frames are JSON-RPC notifications; binary frames are raw PCM.
            if (typeof event.data === 'string') {
                try {
                    this._dispatch(JSON.parse(event.data));
                } catch (e) {
                    console.error('[WS] Parse error:', e);
                }
                return;
            }
            if (event.data instanceof ArrayBuffer) {
                this._dispatchBinary(event.data);
            }
        };

        this.ws.onclose = () => {
            this.connected = false;
            console.log('[WS] Disconnected, reconnecting in', this.reconnectDelay, 'ms');
            if (this.onDisconnect) this.onDisconnect();
            setTimeout(() => this.connect(), this.reconnectDelay);
            this.reconnectDelay = Math.min(this.reconnectDelay * 2, this.maxReconnectDelay);
        };

        this.ws.onerror = (err) => {
            console.error('[WS] Error:', err);
        };
    }

    send(method, params) {
        if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
            console.warn('[WS] Not connected, cannot send');
            return false;
        }
        const msg = { method, params };
        this.ws.send(JSON.stringify(msg));
        return true;
    }

    // Send a pre-built binary frame (microphone PCM).
    sendBinary(data) {
        if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
            console.warn('[WS] Not connected, cannot send binary frame');
            return false;
        }
        this.ws.send(data);
        return true;
    }

    sendWithId(method, params) {
        if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
            return null;
        }
        const id = generateId();
        const msg = { id, method, params };
        this.ws.send(JSON.stringify(msg));
        return id;
    }

    on(method, callback) {
        if (!this.handlers[method]) {
            this.handlers[method] = [];
        }
        this.handlers[method].push(callback);
    }

    _dispatch(msg) {
        // Server notification (has method, no id)
        if (msg.method) {
            const callbacks = this.handlers[msg.method];
            if (callbacks) {
                const params = msg.params ? JSON.parse(typeof msg.params === 'string' ? msg.params : JSON.stringify(msg.params)) : {};
                callbacks.forEach(cb => cb(params));
            }
        }
        // Response to our request (has id and result/error)
        if (msg.id) {
            const responseCallbacks = this.handlers['_response_' + msg.id];
            if (responseCallbacks) {
                responseCallbacks.forEach(cb => cb(msg));
                delete this.handlers['_response_' + msg.id];
            }
        }
    }

    _dispatchBinary(buffer) {
        if (buffer.byteLength < TTS_HEADER_BYTES) return;
        const view = new DataView(buffer);
        if (view.getUint8(0) !== BINARY_TAG_TTS) return;
        const sampleRate = view.getUint32(1, true);
        const flags = view.getUint8(5);
        const durationSeconds = view.getFloat64(6, true);
        const pcm = new Int16Array(buffer, TTS_HEADER_BYTES);

        // Same notification as before, with `audio` kept as a lazy base64 field
        // so the desktop overlay (which only knows the JSON shape) still works.
        const params = {
            sample_rate: sampleRate,
            done: !!(flags & 0x01),
            muted: !!(flags & 0x02),
            duration_seconds: durationSeconds,
            pcm,
        };
        let cachedAudio;
        Object.defineProperty(params, 'audio', {
            enumerable: true,
            get() {
                if (cachedAudio === undefined) {
                    const bytes = new Uint8Array(pcm.buffer, pcm.byteOffset, pcm.byteLength);
                    let binary = '';
                    for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]);
                    cachedAudio = btoa(binary);
                }
                return cachedAudio;
            },
        });

        const callbacks = this.handlers['tts.chunk'];
        if (callbacks) callbacks.forEach(cb => cb(params));
    }

    disconnect() {
        if (this.ws) {
            this.ws.close();
            this.ws = null;
        }
    }
}
