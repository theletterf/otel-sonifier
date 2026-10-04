import { RainEngine } from './rain-engine.js';
import { SoundscapeEngine } from './soundscape-engine.js';

class TelemetryVisualizer {
    constructor() {
        this.rainEngine = new RainEngine();
        this.soundscapeEngine = new SoundscapeEngine();
        this.isAudioEnabled = false;
        this.currentActivity = 0;
        this.raindrops = [];
        this.errorBlooms = [];

        // Each service gets a lane: a band of the screen and a stereo position
        this.lanes = new Map();

        // Sky layers, from calm to stormy, crossfaded by mood
        this.skyLayers = ['sky-low', 'sky-medium', 'sky-high', 'sky-stress']
            .map(id => document.getElementById(id));

        // Combined activity level for soundscape (decays toward 0 when no data)
        this.rawActivityLevel = 0;
        this.lastActivityTime = 0;

        this.initializeUI();
        this.startDataFetching();
        this.setupAnimationLoop();
        this.startSoundscapeDecay();
    }

    initializeUI() {
        const audioToggle = document.getElementById('audio-enabled');
        audioToggle.addEventListener('change', async (e) => {
            this.isAudioEnabled = e.target.checked;
            if (this.isAudioEnabled) {
                await this.rainEngine.initialize();
            } else {
                this.rainEngine.stop();
            }
        });

        // Soundscape controls
        const soundscapeToggle = document.getElementById('soundscape-enabled');
        const soundscapeConfig = document.getElementById('soundscape-config');
        const apiKeyInput = document.getElementById('api-key');
        const promptInput = document.getElementById('soundscape-prompt');
        const setButton = document.getElementById('soundscape-set');
        const statusEl = document.getElementById('soundscape-status');

        // Restore saved values
        const savedKey = localStorage.getItem('gemini-api-key');
        if (savedKey) apiKeyInput.value = savedKey;
        const savedPrompt = localStorage.getItem('soundscape-prompt');
        if (savedPrompt) promptInput.value = savedPrompt;

        this.soundscapeEngine.onStatusChange = (status) => {
            statusEl.className = status;
            const labels = {
                connected: 'Ready',
                playing: 'Playing',
                reconnecting: 'Reconnecting...',
                error: 'Error',
                stopped: '',
                disconnected: '',
            };
            statusEl.textContent = labels[status] || status;
        };

        soundscapeToggle.addEventListener('change', async (e) => {
            if (e.target.checked) {
                soundscapeConfig.style.display = 'block';
                const key = apiKeyInput.value.trim();
                if (key) {
                    localStorage.setItem('gemini-api-key', key);
                    await this._startSoundscape(key, promptInput.value.trim());
                }
            } else {
                soundscapeConfig.style.display = 'none';
                this.soundscapeEngine.stop();
            }
        });

        apiKeyInput.addEventListener('keydown', async (e) => {
            if (e.key === 'Enter') {
                const key = apiKeyInput.value.trim();
                if (key && soundscapeToggle.checked) {
                    localStorage.setItem('gemini-api-key', key);
                    await this._startSoundscape(key, promptInput.value.trim());
                }
            }
        });

        setButton.addEventListener('click', async () => {
            const prompt = promptInput.value.trim();
            localStorage.setItem('soundscape-prompt', prompt);
            if (this.soundscapeEngine.isPlaying) {
                this.soundscapeEngine.setPrompt(prompt);
            } else {
                const key = apiKeyInput.value.trim();
                if (key && soundscapeToggle.checked) {
                    localStorage.setItem('gemini-api-key', key);
                    await this._startSoundscape(key, prompt);
                }
            }
        });
    }

    async _startSoundscape(apiKey, customPrompt) {
        const statusEl = document.getElementById('soundscape-status');
        try {
            statusEl.textContent = 'Connecting...';
            statusEl.className = '';
            await this.soundscapeEngine.connect(apiKey);
            await this.soundscapeEngine.start(this.rawActivityLevel, customPrompt);
        } catch (err) {
            console.error('Soundscape failed to start:', err);
            statusEl.textContent = 'Failed';
            statusEl.className = 'error';
        }
    }

    startDataFetching() {
        // Connect to WebSocket for real-time data streaming
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/ws`;
        
        const connectWebSocket = () => {
            const ws = new WebSocket(wsUrl);
            
            ws.onopen = () => {
                console.log('WebSocket connected - real-time streaming active');
            };
            
            ws.onmessage = (event) => {
                try {
                    const data = JSON.parse(event.data);
                    if (data.type === 'weather') {
                        this.applyWeather(data.payload);
                    }
                } catch (error) {
                    console.error('Error processing WebSocket data:', error);
                }
            };
            
            ws.onclose = (event) => {
                console.log('WebSocket disconnected, attempting to reconnect...');
                // Reconnect after a short delay
                setTimeout(connectWebSocket, 2000);
            };
            
            ws.onerror = (error) => {
                console.error('WebSocket error:', error);
            };
        };
        
        connectWebSocket();
    }

    // Weather arrives every tick (200ms by default). The server has already
    // compared current conditions with the baseline; mood is 0 (calm) to 1
    // (stormy).
    applyWeather(weather) {
        this.currentActivity = weather.mood;

        // Feed mood to the soundscape
        this.rawActivityLevel = weather.mood;
        this.lastActivityTime = Date.now();
        this.soundscapeEngine.updateFromTelemetry(weather.mood);

        this.setSky(weather.mood);
        this.updateLanes(weather.services);
        if (this.isAudioEnabled) {
            this.rainEngine.update(weather);
        }

        document.getElementById('activity-value').textContent =
            `${Math.round(weather.mood * 100)}%`;
        document.getElementById('weather-stats').textContent = this.describe(weather);

        // Spread this tick's drops across the tick so the rain is even
        for (const drop of weather.drops) {
            setTimeout(() => this.createRaindrop(drop), Math.random() * weather.tickMs);
        }

        // Error blooms, more often the worse errors get (at most a few per second)
        if (weather.scores.errors > 0.5 && Math.random() < weather.scores.errors * 0.5) {
            this.createErrorBloom();
        }
    }

    describe(weather) {
        if (weather.rate === 0) return 'No requests';
        const rate = weather.rate < 10 ? weather.rate.toFixed(1) : Math.round(weather.rate);
        const errors = `${Math.round(weather.errorRate * 100)}% errors`;
        const p95 = weather.p95 > 0 ? ` · p95 ${Math.round(weather.p95)}ms` : '';
        return `${rate} req/s · ${errors}${p95}`;
    }

    // Spread the busiest services evenly across the screen, in name order so
    // lanes stay put while rates change. With one service, rain uses the
    // whole width.
    updateLanes(services) {
        const names = services.map(s => s.name).sort();
        this.lanes.clear();
        names.forEach((name, i) => {
            const width = 1 / names.length;
            this.lanes.set(name, { start: i * width, width });
        });
    }

    // Returns where a drop falls (0..1 across the screen) for its service.
    dropPosition(service) {
        const lane = this.lanes.get(service) || { start: 0, width: 1 };
        return lane.start + Math.random() * lane.width;
    }

    // Crossfade the sky layers: each layer is fully visible at its own point
    // on the mood scale and fades out toward its neighbours. The CSS opacity
    // transition smooths changes between ticks.
    setSky(mood) {
        const last = this.skyLayers.length - 1;
        this.skyLayers.forEach((layer, i) => {
            layer.style.opacity = Math.max(0, 1 - Math.abs(mood * last - i));
        });
    }

    createRaindrop(drop) {
        const visualization = document.getElementById('visualization');
        
        // Create raindrop made of trace ID characters
        const raindrop = document.createElement('div');
        raindrop.className = `raindrop ${drop.error ? 'error' : ''}`;
        raindrop.title = `${drop.service} · ${drop.id}`;

        // Use the trace ID characters
        const traceId = drop.id;
        
        // Create individual character elements arranged vertically
        for (let i = 0; i < Math.min(traceId.length, 12); i++) { // Limit to 12 chars for raindrop
            const charElement = document.createElement('div');
            charElement.className = 'raindrop-char';
            charElement.textContent = traceId[i];
            raindrop.appendChild(charElement);
        }
        
        // Horizontal position within the service's lane, start from top
        const position = this.dropPosition(drop.service);
        const leftPercent = position * 95; // Leave some margin
        const startY = -50 - Math.random() * 100; // Start above viewport
        
        // Position raindrop
        raindrop.style.left = `${leftPercent}%`;
        raindrop.style.top = `${startY}px`;
        raindrop.style.width = '12px'; // Narrow like a raindrop
        
        visualization.appendChild(raindrop);
        this.raindrops.push(raindrop);
        
        // Start falling animation
        raindrop.classList.add('raindrop-fall');
        
        // Remove after animation completes
        setTimeout(() => {
            if (raindrop.parentNode) {
                // Play raindrop sound when hitting ground
                if (this.isAudioEnabled) {
                    // Sound comes from where the drop lands
                    this.rainEngine.playRaindropSound(position * 2 - 1, drop.error);
                }
                
                // Create ground splash effect
                this.createGroundSplash(leftPercent);
                raindrop.parentNode.removeChild(raindrop);
            }
            
            const dropIndex = this.raindrops.indexOf(raindrop);
            if (dropIndex > -1) this.raindrops.splice(dropIndex, 1);
        }, 3000);
    }

    createGroundSplash(leftPercent) {
        const visualization = document.getElementById('visualization');
        const splash = document.createElement('div');
        splash.className = 'ground-splash';
        splash.textContent = '•';
        
        // Position at bottom where raindrop lands
        splash.style.left = `${leftPercent}%`;
        splash.style.bottom = '0px';
        splash.style.position = 'absolute';
        splash.style.color = 'rgba(135, 206, 250, 0.6)';
        splash.style.fontSize = '16px';
        splash.style.pointerEvents = 'none';
        
        visualization.appendChild(splash);
        
        // Remove after splash animation
        setTimeout(() => {
            if (splash.parentNode) {
                splash.parentNode.removeChild(splash);
            }
        }, 500);
    }


    createErrorBloom() {
        const visualization = document.getElementById('visualization');
        const bloom = document.createElement('div');
        bloom.className = 'error-bloom';
        
        // Random position
        const leftPercent = Math.random() * 100;
        const topPercent = Math.random() * 100;
        
        bloom.style.left = `${leftPercent}%`;
        bloom.style.top = `${topPercent}%`;
        
        visualization.appendChild(bloom);
        this.errorBlooms.push(bloom);
        
        // Remove after animation
        setTimeout(() => {
            if (bloom.parentNode) {
                bloom.parentNode.removeChild(bloom);
            }
            const index = this.errorBlooms.indexOf(bloom);
            if (index > -1) this.errorBlooms.splice(index, 1);
        }, 1000);
    }


    startSoundscapeDecay() {
        // When no telemetry arrives for 3s, decay toward 0
        setInterval(() => {
            if (Date.now() - this.lastActivityTime > 3000 && this.rawActivityLevel > 0.01) {
                this.rawActivityLevel *= 0.85;
                if (this.rawActivityLevel < 0.01) this.rawActivityLevel = 0;
                this.soundscapeEngine.updateFromTelemetry(this.rawActivityLevel);
            }
        }, 1000);
    }

    setupAnimationLoop() {
        // Clean up old visual elements periodically
        setInterval(() => {
            // Remove old raindrops
            this.raindrops = this.raindrops.filter(drop => drop.parentNode);
            
            // Remove old blooms
            this.errorBlooms = this.errorBlooms.filter(bloom => bloom.parentNode);
        }, 5000);
    }
}

// Initialize when page loads
document.addEventListener('DOMContentLoaded', function() {
    new TelemetryVisualizer();
});