// Chat UI logic — DOM-safe, no innerHTML with user text

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
        this.inputEl.addEventListener('input', () => {
            this.inputEl.style.height = 'auto';
            this.inputEl.style.height = Math.min(this.inputEl.scrollHeight, 120) + 'px';
        });

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
        const welcome = this.messagesEl.querySelector('.welcome-message');
        if (welcome) welcome.remove();

        const el = document.createElement('div');
        el.className = 'message user';

        const avatar = document.createElement('div');
        avatar.className = 'message-avatar';
        avatar.textContent = 'You';

        const content = document.createElement('div');
        content.className = 'message-content';
        content.textContent = text;

        el.appendChild(avatar);
        el.appendChild(content);
        this.messagesEl.appendChild(el);
        this._scrollToBottom();
    }

    startAgentMessage() {
        const el = document.createElement('div');
        el.className = 'message assistant';

        const avatar = document.createElement('div');
        avatar.className = 'message-avatar';
        avatar.textContent = 'M';

        const content = document.createElement('div');
        content.className = 'message-content streaming-cursor';

        el.appendChild(avatar);
        el.appendChild(content);
        this.messagesEl.appendChild(el);
        this.currentStreamingEl = content;
        this._scrollToBottom();
        return el;
    }

    streamToken(token) {
        if (!this.currentStreamingEl) {
            this.startAgentMessage();
        }
        // Append escaped text safely via textContent
        this.currentStreamingEl.appendChild(document.createTextNode(token));
        this._scrollToBottom();
    }

    finalizeMessage() {
        if (this.currentStreamingEl) {
            this.currentStreamingEl.classList.remove('streaming-cursor');
            // Render markdown via DOMParser (safe HTML parsing)
            const text = this.currentStreamingEl.textContent;
            const md = renderMarkdown(text);
            const frag = new DOMParser().parseFromString(md, 'text/html').body;
            this.currentStreamingEl.textContent = '';
            while (frag.firstChild) {
                this.currentStreamingEl.appendChild(frag.firstChild);
            }
            this.currentStreamingEl = null;
        }
        this.sendBtn.disabled = false;
        this.inputEl.focus();
    }

    addSystemMessage(text) {
        const el = document.createElement('div');
        el.className = 'message assistant';

        const avatar = document.createElement('div');
        avatar.className = 'message-avatar';
        avatar.textContent = 'M';

        const content = document.createElement('div');
        content.className = 'message-content';
        const em = document.createElement('em');
        em.textContent = text;
        content.appendChild(em);

        el.appendChild(avatar);
        el.appendChild(content);
        this.messagesEl.appendChild(el);
        this._scrollToBottom();
    }

    _scrollToBottom() {
        requestAnimationFrame(() => {
            this.messagesEl.scrollTop = this.messagesEl.scrollHeight;
        });
    }
}
