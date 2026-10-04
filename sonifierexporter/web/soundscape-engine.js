const WS_URL = 'wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1alpha.GenerativeService.BidiGenerateMusic';
const MODEL = 'models/lyria-realtime-exp';
const SAMPLE_RATE = 48000;

// Fixed structural params — no RESET_CONTEXT ever
const FIXED_BPM = 88;
const FIXED_SCALE = "C_MAJOR_A_MINOR";

// Single prompt — the soundscape identity never changes.
// Density and brightness do all the expressive work.
const SOUNDSCAPE_PROMPT = "Ambient electronic soundscape, soft synth pads, atmospheric textures, gentle evolving tones";

// Min change in metricLevel to trigger an update
const UPDATE_THRESHOLD = 0.01;

export class SoundscapeEngine {
    constructor() {
        this.ws = null;
        this.apiKey = null;
        this.audioContext = null;
        this.isConnected = false;
        this.isPlaying = false;
        this.nextPlayTime = 0;
        this.lastMetricLevel = -1;
        this.currentPrompt = SOUNDSCAPE_PROMPT;
        this.onStatusChange = null;
    }

    async connect(apiKey) {
        this.apiKey = apiKey;

        if (!this.audioContext) {
            this.audioContext = new (window.AudioContext || window.webkitAudioContext)({
                sampleRate: SAMPLE_RATE
            });
        }

        return new Promise((resolve, reject) => {
            const url = `${WS_URL}?key=${encodeURIComponent(apiKey)}`;
            console.log('Lyria: connecting...');
            this.ws = new WebSocket(url);
            this.ws.binaryType = 'arraybuffer';

            const timeout = setTimeout(() => {
                console.error('Lyria: connection timeout');
                reject(new Error('Connection timeout'));
                this.ws.close();
            }, 10000);

            this.ws.onopen = () => {
                console.log('Lyria: WebSocket opened, sending setup');
                this.ws.send(JSON.stringify({
                    setup: { model: MODEL }
                }));
            };

            this.ws.onmessage = async (event) => {
                let text;
                if (typeof event.data === 'string') {
                    text = event.data;
                } else if (event.data instanceof ArrayBuffer) {
                    text = new TextDecoder().decode(event.data);
                } else if (event.data instanceof Blob) {
                    text = await event.data.text();
                } else {
                    return;
                }

                let msg;
                try {
                    msg = JSON.parse(text);
                } catch {
                    return;
                }

                if (msg.setupComplete) {
                    console.log('Lyria: setup complete');
                    clearTimeout(timeout);
                    this.isConnected = true;
                    this._setStatus('connected');
                    resolve();
                }

                if (msg.serverContent?.audioChunks) {
                    for (const chunk of msg.serverContent.audioChunks) {
                        this._scheduleAudioChunk(chunk.data);
                    }
                }

                if (msg.filteredPrompt) {
                    console.warn('Lyria prompt filtered:', msg.filteredPrompt.filteredReason);
                }
                if (msg.warning) {
                    console.warn('Lyria warning:', msg.warning);
                }
            };

            this.ws.onclose = (event) => {
                clearTimeout(timeout);
                console.log('Lyria: closed, code:', event.code, 'reason:', event.reason);
                const wasPlaying = this.isPlaying;
                this.isConnected = false;
                this.isPlaying = false;

                if (wasPlaying && this.apiKey) {
                    this._setStatus('reconnecting');
                    setTimeout(() => this._reconnect(), 2000);
                } else {
                    this._setStatus('disconnected');
                }
            };

            this.ws.onerror = (err) => {
                clearTimeout(timeout);
                console.error('Lyria WebSocket error:', err);
                this._setStatus('error');
                reject(err);
            };
        });
    }

    async _reconnect() {
        const savedLevel = this.lastMetricLevel;
        const savedPrompt = this.currentPrompt;
        try {
            await this.connect(this.apiKey);
            await this.start(savedLevel >= 0 ? savedLevel : 0, savedPrompt);
        } catch (e) {
            console.error('Lyria reconnect failed:', e);
            this._setStatus('error');
        }
    }

    async start(metricLevel = 0, customPrompt) {
        if (!this.isConnected) return;

        this.nextPlayTime = this.audioContext.currentTime;
        this.lastMetricLevel = metricLevel;

        // Use custom prompt if provided, otherwise default
        this.currentPrompt = customPrompt || SOUNDSCAPE_PROMPT;
        this._sendPrompts([{ text: this.currentPrompt, weight: 1.0 }]);

        // Send initial config with fixed structure
        this._sendConfig({
            bpm: FIXED_BPM,
            scale: FIXED_SCALE,
            ...this._levelToConfig(metricLevel),
        });

        this._sendPlayback('PLAY');
        this.isPlaying = true;
        this._setStatus('playing');
    }

    updateFromTelemetry(metricLevel) {
        if (!this.isPlaying) return;
        if (Math.abs(metricLevel - this.lastMetricLevel) < UPDATE_THRESHOLD) return;

        this.lastMetricLevel = metricLevel;

        const config = this._levelToConfig(metricLevel);
        this._sendConfig(config);

        console.log(`Lyria: level=${metricLevel.toFixed(2)} density=${config.density.toFixed(2)} brightness=${config.brightness.toFixed(2)} guidance=${config.guidance.toFixed(1)} temp=${config.temperature.toFixed(1)}`);
    }

    setPrompt(prompt) {
        this.currentPrompt = prompt || SOUNDSCAPE_PROMPT;
        if (this.isPlaying) {
            this._sendPrompts([{ text: this.currentPrompt, weight: 1.0 }]);
            console.log('Lyria: prompt updated');
        }
    }

    _levelToConfig(level) {
        return {
            density: 0.05 + level * 0.95,      // 0.05 → 1.0  (near-silent to packed)
            brightness: 0.85 - level * 0.75,    // 0.85 → 0.10 (bright/airy to dark/heavy)
            guidance: 1.0 + level * 5.0,        // 1.0  → 6.0  (loose to intense)
            temperature: 0.7 + level * 1.3,     // 0.7  → 2.0  (steady to chaotic)
            topK: Math.round(250 - level * 200), // 250  → 50   (diverse to focused)
        };
    }

    _sendPrompts(prompts) {
        if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
        this.ws.send(JSON.stringify({
            client_content: {
                weightedPrompts: prompts
            }
        }));
    }

    _sendConfig(config) {
        if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
        this.ws.send(JSON.stringify({
            music_generation_config: config
        }));
    }

    _sendPlayback(action) {
        if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
        this.ws.send(JSON.stringify({
            playback_control: action
        }));
    }

    _scheduleAudioChunk(base64Data) {
        if (!this.audioContext || !this.isPlaying) return;

        const binaryStr = atob(base64Data);
        const bytes = new Uint8Array(binaryStr.length);
        for (let i = 0; i < binaryStr.length; i++) {
            bytes[i] = binaryStr.charCodeAt(i);
        }

        const int16 = new Int16Array(bytes.buffer);
        const numSamples = int16.length / 2;
        const audioBuffer = this.audioContext.createBuffer(2, numSamples, SAMPLE_RATE);
        const left = audioBuffer.getChannelData(0);
        const right = audioBuffer.getChannelData(1);

        for (let i = 0; i < numSamples; i++) {
            left[i] = int16[i * 2] / 32768;
            right[i] = int16[i * 2 + 1] / 32768;
        }

        const now = this.audioContext.currentTime;
        if (this.nextPlayTime < now) {
            this.nextPlayTime = now + 0.05;
        }

        const source = this.audioContext.createBufferSource();
        source.buffer = audioBuffer;
        source.connect(this.audioContext.destination);
        source.start(this.nextPlayTime);
        this.nextPlayTime += audioBuffer.duration;
    }

    _setStatus(status) {
        console.log('Soundscape:', status);
        if (this.onStatusChange) {
            this.onStatusChange(status);
        }
    }

    async stop() {
        this.isPlaying = false;
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this._sendPlayback('STOP');
            this.ws.close();
        }
        this.ws = null;
        this.isConnected = false;
        this.lastMetricLevel = -1;
        this._setStatus('stopped');
    }

    disconnect() {
        this.apiKey = null;
        this.stop();
        if (this.audioContext) {
            this.audioContext.close();
            this.audioContext = null;
        }
    }
}
