const WS_URL = 'wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1alpha.GenerativeService.BidiGenerateMusic';
const MODEL = 'models/lyria-realtime-exp';
const SAMPLE_RATE = 48000;

// Fixed structural params — no RESET_CONTEXT ever. Lyria replaces the whole
// config on every update, so these are sent each time to keep them set.
const FIXED_BPM = 88;
const FIXED_SCALE = "C_MAJOR_A_MINOR";

// Moderate, fixed guidance: higher guidance makes transitions more abrupt.
const GUIDANCE = 3.0;

// The base prompt (default or the user's) is the soundscape's identity. As the
// mood worsens, a storm prompt is blended in with growing weight, which is how
// Lyria morphs music smoothly.
const SOUNDSCAPE_PROMPT = "Ambient electronic soundscape, soft synth pads, atmospheric textures, gentle evolving tones";
const STORM_PROMPT = "Tense and turbulent, dark rumbling drones, driving percussion, dissonant swells";
const STORM_MIN_LEVEL = 0.05;
const STORM_MAX_WEIGHT = 1.5;

// Mood arrives several times a second; Lyria gets at most one update per
// interval, and only for a noticeable change.
const UPDATE_INTERVAL_MS = 2000;
const UPDATE_THRESHOLD = 0.03;

export class SoundscapeEngine {
    constructor() {
        this.ws = null;
        this.apiKey = null;
        this.audioContext = null;
        this.isConnected = false;
        this.isPlaying = false;
        this.nextPlayTime = 0;
        this.lastMetricLevel = -1;
        this.pendingLevel = 0;
        this.updateTimer = null;
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
        // Resume at the latest mood, not the last one sent before the drop.
        const savedLevel = this.pendingLevel;
        const savedPrompt = this.currentPrompt;
        try {
            await this.connect(this.apiKey);
            await this.start(savedLevel, savedPrompt);
        } catch (e) {
            console.error('Lyria reconnect failed:', e);
            this._setStatus('error');
        }
    }

    async start(metricLevel = 0, customPrompt) {
        if (!this.isConnected) return;

        this.nextPlayTime = this.audioContext.currentTime;

        // Use custom prompt if provided, otherwise default
        this.currentPrompt = customPrompt || SOUNDSCAPE_PROMPT;
        this.pendingLevel = metricLevel;
        this._applyLevel(metricLevel);

        this._sendPlayback('PLAY');
        this.isPlaying = true;
        this._setStatus('playing');

        clearInterval(this.updateTimer);
        this.updateTimer = setInterval(() => this._flushLevel(), UPDATE_INTERVAL_MS);
    }

    updateFromTelemetry(metricLevel) {
        this.pendingLevel = metricLevel;
    }

    _flushLevel() {
        if (!this.isPlaying) return;
        if (Math.abs(this.pendingLevel - this.lastMetricLevel) < UPDATE_THRESHOLD) return;
        this._applyLevel(this.pendingLevel);
    }

    // Sends the prompts and the full config for a level.
    _applyLevel(level) {
        this.lastMetricLevel = level;
        this._sendPrompts(this._levelToPrompts(level));
        const config = this._levelToConfig(level);
        this._sendConfig(config);
        console.log(`Lyria: level=${level.toFixed(2)} storm=${(this._stormWeight(level)).toFixed(2)} density=${config.density.toFixed(2)} brightness=${config.brightness.toFixed(2)} temp=${config.temperature.toFixed(2)}`);
    }

    setPrompt(prompt) {
        this.currentPrompt = prompt || SOUNDSCAPE_PROMPT;
        if (this.isPlaying) {
            this._sendPrompts(this._levelToPrompts(this.lastMetricLevel));
            console.log('Lyria: prompt updated');
        }
    }

    _stormWeight(level) {
        return level < STORM_MIN_LEVEL ? 0 : level * STORM_MAX_WEIGHT;
    }

    _levelToPrompts(level) {
        const prompts = [{ text: this.currentPrompt, weight: 1.0 }];
        const storm = this._stormWeight(level);
        if (storm > 0) prompts.push({ text: STORM_PROMPT, weight: storm });
        return prompts;
    }

    _levelToConfig(level) {
        return {
            bpm: FIXED_BPM,
            scale: FIXED_SCALE,
            guidance: GUIDANCE,
            density: 0.1 + level * 0.85,        // 0.1 → 0.95 (sparse to busy)
            brightness: 0.7 - level * 0.5,      // 0.7 → 0.2  (airy to dark)
            temperature: 1.0 + level * 0.4,     // 1.0 → 1.4  (steady to restless)
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
        clearInterval(this.updateTimer);
        this.updateTimer = null;
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
