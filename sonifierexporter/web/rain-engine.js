// Rain sounds, synthesized with Web Audio:
// - Drops: short noise bursts, one per raindrop landing, panned by service.
//   Failed requests land with a duller, lower tap.
// - Bed: continuous rain hiss whose loudness follows the request rate, so
//   heavy traffic sounds like heavy rain even when drops are sampled.
// - Air: a low-pass filter over everything that closes as latency rises
//   above its baseline, so a slow system sounds muffled.
// - Thunder: a rare, distant rumble while the error rate is high.

const MAX_DROP_VOICES = 24;
const DROP_BUFFER_COUNT = 4;
const DROP_SAMPLES = 1024;

const BED_MAX_GAIN = 0.12;
const AIR_OPEN_HZ = 12000;
const AIR_MUFFLED_HZ = 900;

const THUNDER_MIN_ERRORS = 0.5;
const THUNDER_COOLDOWN_S = 15;
// Chance per weather update (5 per second) once the cooldown has passed.
const THUNDER_CHANCE = 0.1;

export class RainEngine {
    constructor() {
        this.audioContext = null;
        this.isInitialized = false;
    }

    async initialize() {
        try {
            const ctx = new (window.AudioContext || window.webkitAudioContext)();
            this.audioContext = ctx;

            this.master = ctx.createGain();
            this.master.gain.value = 0.8;
            this.master.connect(ctx.destination);

            // Everything except thunder passes through the air filter.
            this.air = ctx.createBiquadFilter();
            this.air.type = 'lowpass';
            this.air.frequency.value = AIR_OPEN_HZ;
            this.air.connect(this.master);

            // Pre-rendered drop sounds: decaying noise bursts.
            this.dropBuffers = [];
            for (let b = 0; b < DROP_BUFFER_COUNT; b++) {
                const buffer = ctx.createBuffer(1, DROP_SAMPLES, ctx.sampleRate);
                const data = buffer.getChannelData(0);
                for (let i = 0; i < DROP_SAMPLES; i++) {
                    data[i] = (Math.random() * 2 - 1) * (1 - i / DROP_SAMPLES);
                }
                this.dropBuffers.push(buffer);
            }
            this.activeDrops = 0;

            // Two seconds of looping noise for the bed and thunder.
            this.noise = ctx.createBuffer(1, ctx.sampleRate * 2, ctx.sampleRate);
            const noise = this.noise.getChannelData(0);
            for (let i = 0; i < noise.length; i++) noise[i] = Math.random() * 2 - 1;

            const bed = ctx.createBufferSource();
            bed.buffer = this.noise;
            bed.loop = true;
            const bedShape = ctx.createBiquadFilter();
            bedShape.type = 'bandpass';
            bedShape.frequency.value = 3000;
            bedShape.Q.value = 0.5;
            this.bedGain = ctx.createGain();
            this.bedGain.gain.value = 0;
            bed.connect(bedShape).connect(this.bedGain).connect(this.air);
            bed.start();

            this.lastThunder = -Infinity;
            this.isInitialized = true;
            console.log('Rain engine initialized');
            return true;
        } catch (error) {
            console.error('Failed to initialize rain engine:', error);
            return false;
        }
    }

    // pan is -1 (left) to 1 (right).
    playRaindropSound(pan = 0, error = false) {
        const ctx = this.audioContext;
        if (!ctx || this.activeDrops >= MAX_DROP_VOICES) return;

        const now = ctx.currentTime;
        const source = ctx.createBufferSource();
        source.buffer = this.dropBuffers[Math.floor(Math.random() * DROP_BUFFER_COUNT)];

        // High-pass filter to make it sound like water
        const filter = ctx.createBiquadFilter();
        filter.type = 'highpass';
        filter.frequency.value = error ? 800 + Math.random() * 800 : 2000 + Math.random() * 3000;
        filter.Q.value = 5;

        // Quick envelope
        const gain = ctx.createGain();
        const volume = 0.05 + Math.random() * 0.05;
        gain.gain.setValueAtTime(0, now);
        gain.gain.linearRampToValueAtTime(volume, now + 0.001);
        gain.gain.exponentialRampToValueAtTime(0.001, now + 0.05);

        const panner = ctx.createStereoPanner();
        panner.pan.value = Math.max(-1, Math.min(1, pan));

        source.connect(filter).connect(gain).connect(panner).connect(this.air);
        this.activeDrops++;
        source.onended = () => { this.activeDrops--; };
        source.start(now);
        source.stop(now + 0.05);
    }

    // Called on every weather update.
    update(weather) {
        const ctx = this.audioContext;
        if (!ctx) return;
        const now = ctx.currentTime;

        // Loudness on a log scale: silent at 0 req/s, full at 1000 req/s.
        const rain = Math.min(1, Math.log10(1 + weather.rate) / 3);
        this.bedGain.gain.setTargetAtTime(BED_MAX_GAIN * rain, now, 1.0);

        const cutoff = AIR_OPEN_HZ * Math.pow(AIR_MUFFLED_HZ / AIR_OPEN_HZ, weather.scores.latency);
        this.air.frequency.setTargetAtTime(cutoff, now, 1.5);

        const errors = weather.scores.errors;
        if (errors > THUNDER_MIN_ERRORS &&
            now - this.lastThunder > THUNDER_COOLDOWN_S &&
            Math.random() < errors * THUNDER_CHANCE) {
            this.lastThunder = now;
            this.thunder(errors);
        }
    }

    thunder(intensity) {
        const ctx = this.audioContext;
        const now = ctx.currentTime;
        const duration = 4 + Math.random() * 2;

        const source = ctx.createBufferSource();
        source.buffer = this.noise;
        source.loop = true;
        const filter = ctx.createBiquadFilter();
        filter.type = 'lowpass';
        filter.frequency.value = 90 + Math.random() * 60;
        const gain = ctx.createGain();
        const peak = 0.3 + 0.3 * intensity;
        gain.gain.setValueAtTime(0.0001, now);
        gain.gain.exponentialRampToValueAtTime(peak, now + 0.4);
        gain.gain.exponentialRampToValueAtTime(0.0001, now + duration);
        const panner = ctx.createStereoPanner();
        panner.pan.value = Math.random() * 1.2 - 0.6;

        source.connect(filter).connect(gain).connect(panner).connect(this.master);
        source.start(now);
        source.stop(now + duration);
    }

    stop() {
        if (this.audioContext) {
            this.audioContext.close();
            this.audioContext = null;
            this.isInitialized = false;
        }
    }
}
