export class RainEngine {
    constructor() {
        this.audioContext = null;
        this.isInitialized = false;
    }

    async initialize() {
        try {
            this.audioContext = new (window.AudioContext || window.webkitAudioContext)();
            
            this.isInitialized = true;
            console.log('Rain engine initialized');
            return true;
        } catch (error) {
            console.error('Failed to initialize rain engine:', error);
            return false;
        }
    }

    playRaindropSound() {
        if (!this.audioContext) return;
        
        const currentTime = this.audioContext.currentTime;
        
        // Create filtered noise burst for raindrop
        const noiseSource = this.audioContext.createBufferSource();
        const filter = this.audioContext.createBiquadFilter();
        const gainNode = this.audioContext.createGain();
        
        // Very short noise burst
        const shortBuffer = this.audioContext.createBuffer(1, 1024, this.audioContext.sampleRate);
        const output = shortBuffer.getChannelData(0);
        for (let i = 0; i < 1024; i++) {
            output[i] = (Math.random() * 2 - 1) * (1 - i / 1024); // Decaying noise
        }
        
        noiseSource.buffer = shortBuffer;
        
        // High-pass filter to make it sound like water
        filter.type = 'highpass';
        filter.frequency.setValueAtTime(2000 + Math.random() * 3000, currentTime);
        filter.Q.setValueAtTime(5, currentTime);
        
        // Quick envelope
        const volume = 0.05 + Math.random() * 0.05;
        gainNode.gain.setValueAtTime(0, currentTime);
        gainNode.gain.linearRampToValueAtTime(volume, currentTime + 0.001);
        gainNode.gain.exponentialRampToValueAtTime(0.001, currentTime + 0.05);
        
        noiseSource.connect(filter);
        filter.connect(gainNode);
        gainNode.connect(this.audioContext.destination);
        
        noiseSource.start(currentTime);
        noiseSource.stop(currentTime + 0.05);
    }



    stop() {
        if (this.audioContext) {
            this.audioContext.close();
            this.audioContext = null;
            this.isInitialized = false;
        }
    }
}