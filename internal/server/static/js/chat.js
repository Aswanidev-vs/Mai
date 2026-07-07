// Chat UI logic

class ChatUI {
    constructor() {
        this.messagesEl = document.getElementById('chatMessages');
        this.inputEl = document.getElementById('chatInput');
        this.sendBtn = document.getElementById('sendBtn');
        this.currentStreamingEl = null;
        this.onSend = null;

        this._setupInput();
    }

    _setupInput() {
        // Auto-resize textarea
        this.inputEl.addEventListener('input', () => {
            this.inputEl.style.height = 'auto';
            this.inputEl.style.height = Math.min(this.inputEl.scrollHeight, 120) + 'px';
        });

        // Enter to send
        this.inputEl.addEventListener('keydown', (e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                this._send();
            }
        });

        this.sendBtn.addEventListener('click', () => this._send());
    }

    _send() {
        const text = this.inputEl.value.trim();
        if (!text) return;

        this.addUserMessage(text);
        this.inputEl.value = '';
        this.inputEl.style.height = 'auto';
        this.sendBtn.disabled = true;

        if (this.onSend) this.onSend(text);
    }

    addUserMessage(text) {
        // Remove welcome message
        const welcome = this.messagesEl.querySelector('.welcome-message');
        if (welcome) welcome.remove();

        const el = document.createElement('div');
        el.className = 'message user';
        el.innerHTML = `
            <div class="message-avatar">You</div>
            <div class="message-content">${escapeHtml(text)}</div>
        `;
        this.messagesEl.appendChild(el);
        this._scrollToBottom();
    }

    startAgentMessage() {
        const el = document.createElement('div');
        el.className = 'message assistant';
        el.innerHTML = `
            <div class="message-avatar">M</div>
            <div class="message-content streaming-cursor"></div>
        `;
        this.messagesEl.appendChild(el);
        this.currentStreamingEl = el.querySelector('.message-content');
        this._scrollToBottom();
        return el;
    }

    streamToken(token) {
        if (!this.currentStreamingEl) {
            this.startAgentMessage();
        }
        this.currentStreamingEl.innerHTML += escapeHtml(token);
        this._scrollToBottom();
    }

    finalizeMessage() {
        if (this.currentStreamingEl) {
            this.currentStreamingEl.classList.remove('streaming-cursor');
            // Re-render with markdown
            const text = this.currentStreamingEl.textContent;
            this.currentStreamingEl.innerHTML = renderMarkdown(text);
            this.currentStreamingEl = null;
        }
        this.sendBtn.disabled = false;
        this.inputEl.focus();
    }

    addSystemMessage(text) {
        const el = document.createElement('div');
        el.className = 'message assistant';
        el.innerHTML = `
            <div class="message-avatar">M</div>
            <div class="message-content"><em>${escapeHtml(text)}</em></div>
        `;
        this.messagesEl.appendChild(el);
        this._scrollToBottom();
    }

    _scrollToBottom() {
        requestAnimationFrame(() => {
            this.messagesEl.scrollTop = this.messagesEl.scrollHeight;
        });
    }
}
