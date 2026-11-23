// AudioWorklet processor: forwards 256-frame mono buffers to the main thread.
// 256 frames at 16 kHz is ~16 ms, down from ScriptProcessor's 2048 (~128 ms).
class PCMCaptureProcessor extends AudioWorkletProcessor {
    constructor() {
        super();
        this._buf = new Float32Array(256);
        this._offset = 0;
    }

    process(inputs) {
        const channel = inputs[0] && inputs[0][0];
        if (!channel) return true;
        for (let i = 0; i < channel.length; i++) {
            this._buf[this._offset++] = channel[i];
            if (this._offset === this._buf.length) {
                // Transfer instead of copy; the worklet keeps running on a fresh buffer.
                const out = this._buf;
                this.port.postMessage(out, [out.buffer]);
                this._buf = new Float32Array(256);
                this._offset = 0;
            }
        }
        return true;
    }
}

registerProcessor('pcm-capture', PCMCaptureProcessor);
