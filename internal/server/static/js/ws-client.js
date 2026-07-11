// WebSocket client with auto-reconnect and message routing

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

        this.ws.onopen = () => {
            this.connected = true;
            this.reconnectDelay = 1000;
            console.log('[WS] Connected');
            if (this.onConnect) this.onConnect();
        };

        this.ws.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                this._dispatch(msg);
            } catch (e) {
                console.error('[WS] Parse error:', e);
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
            return;
        }
        const msg = { method, params };
        this.ws.send(JSON.stringify(msg));
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

    disconnect() {
        if (this.ws) {
            this.ws.close();
            this.ws = null;
        }
    }
}
