import { RainEngine } from './rain-engine.js';
import { TelemetryAnalyzer } from './telemetry-analyzer.js';
import { SoundscapeEngine } from './soundscape-engine.js';

class TelemetryVisualizer {
    constructor() {
        this.rainEngine = new RainEngine();
        this.soundscapeEngine = new SoundscapeEngine();
        this.telemetryAnalyzer = new TelemetryAnalyzer();
        this.isAudioEnabled = false;
        this.currentActivity = 0;
        this.raindrops = [];
        this.errorBlooms = [];

        // Constant rain system
        this.traceQueue = [];
        this.basePlaybackRate = 20; // Base: 20ms between drops (50/sec)
        this.currentPlaybackRate = this.basePlaybackRate;
        this.lastTraceCount = 0;

        // Target metric level tracking
        this.targetMetricLevel = 0;
        this.currentSkyId = 'sky-low';

        // Combined activity level for soundscape (decays toward 0 when no data)
        this.rawActivityLevel = 0;
        this.lastActivityTime = 0;

        this.initializeUI();
        this.startDataFetching();
        this.setupAnimationLoop();
        this.startConstantRain();
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
                    if (data.payload) {
                        const analyzedTelemetry = this.telemetryAnalyzer.analyzeTelemetry(data.payload);
                        this.updateVisualization(analyzedTelemetry, data.type);
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

    updateVisualization(telemetry, dataType) {
        // Calculate individual activities
        const traceActivity = Math.min(telemetry.traces.count / 10, 1); // More sensitive to traces
        const metricActivity = Math.max(
            telemetry.metrics.cpu / 100,
            telemetry.metrics.memory / 100,
            telemetry.metrics.disk / 100
        );
        const logActivity = Math.min(telemetry.logs.totalCount / 10, 1); // Count total logs, not just errors
        
        // Update current activity — tracks new data quickly, decays when idle
        const newActivity = Math.max(traceActivity, metricActivity, logActivity);
        if (newActivity >= this.currentActivity) {
            // Jump up immediately to new highs
            this.currentActivity = newActivity;
        } else {
            // Gentle decay when activity drops (0.9 per update instead of 0.95)
            this.currentActivity = Math.max(this.currentActivity * 0.9, newActivity);
        }

        // Feed combined activity to soundscape (all signals)
        this.rawActivityLevel = this.currentActivity;
        this.lastActivityTime = Date.now();
        this.soundscapeEngine.updateFromTelemetry(this.currentActivity);

        // Sky gradient driven by metrics (discrete levels)
        if (dataType === 'metrics' && metricActivity > 0) {
            let detectedLevel;
            if (metricActivity < 0.2) {
                detectedLevel = 0.1;
            } else if (metricActivity < 0.45) {
                detectedLevel = 0.3;
            } else if (metricActivity < 0.8) {
                detectedLevel = 0.6;
            } else {
                detectedLevel = 1.0;
            }
            if (detectedLevel !== this.targetMetricLevel) {
                this.targetMetricLevel = detectedLevel;
                this.updateSkyGradient(this.targetMetricLevel);
            }
        }
        
        // Update activity display
        document.getElementById('activity-value').textContent = 
            `${Math.round(this.currentActivity * 100)}%`;
        
        // Update audio - rain engine handles its own activity updates
        
        // Buffer incoming traces for constant rain playback
        if (dataType === 'traces' && telemetry.traces.count > 0 && telemetry.traces.traceIds) {
            // Add all new traces to the queue
            telemetry.traces.traceIds.forEach(trace => {
                this.traceQueue.push(trace);
            });
            
            // Store current trace count for rate adjustment
            this.lastTraceCount = telemetry.traces.count;
            
            // Adjust playback rate based on queue length to prevent overflow/underflow
            this.adjustPlaybackRate();
            
            console.log(`Buffered ${telemetry.traces.count} traces. Queue: ${this.traceQueue.length}, Rate: ${this.currentPlaybackRate}ms`);
        }
        
        // Create error blooms for high error rates
        if (telemetry.traces.errorRate > 0.3 || telemetry.logs.errorRate > 0.5) {
            this.createErrorBloom();
        }
    }

    updateSkyGradient(targetLevel) {
        // Map target levels to sky layer IDs
        let targetSkyId;
        
        if (targetLevel <= 0.1) {
            targetSkyId = 'sky-low';
        } else if (targetLevel <= 0.3) {
            targetSkyId = 'sky-medium';
        } else if (targetLevel <= 0.6) {
            targetSkyId = 'sky-high';
        } else {
            targetSkyId = 'sky-stress';
        }
        
        // Only update if sky level has actually changed
        if (targetSkyId !== this.currentSkyId) {
            console.log(`Sky transition: level ${targetLevel} -> ${targetSkyId}`);
            
            // Fade out all sky layers
            const allLayers = document.querySelectorAll('.sky-layer');
            allLayers.forEach(layer => layer.classList.remove('active'));
            
            // Fade in the target sky layer
            const targetLayer = document.getElementById(targetSkyId);
            if (targetLayer) {
                targetLayer.classList.add('active');
            }
            
            this.currentSkyId = targetSkyId;
        }
    }


    createRaindrop(trace) {
        const visualization = document.getElementById('visualization');
        
        // Create raindrop made of trace ID characters
        const raindrop = document.createElement('div');
        raindrop.className = `raindrop ${trace.isError ? 'error' : ''}`;
        
        // Use the trace ID characters
        const traceId = trace.id || trace.shortId;
        
        // Create individual character elements arranged vertically
        for (let i = 0; i < Math.min(traceId.length, 12); i++) { // Limit to 12 chars for raindrop
            const charElement = document.createElement('div');
            charElement.className = 'raindrop-char';
            charElement.textContent = traceId[i];
            raindrop.appendChild(charElement);
        }
        
        // Random horizontal position, start from top
        const leftPercent = Math.random() * 95; // Leave some margin
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
                    this.rainEngine.playRaindropSound();
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


    adjustPlaybackRate() {
        const queueLength = this.traceQueue.length;
        
        // Adjust rate based on queue length to handle high-volume stress testing
        if (queueLength > 200) {
            // Very large queue - maximum speed
            this.currentPlaybackRate = 1; // 1ms = 1000 drops/sec
        } else if (queueLength > 100) {
            // Large queue - very fast playback
            this.currentPlaybackRate = 2; // 2ms = 500 drops/sec
        } else if (queueLength > 50) {
            // Queue getting full - fast playback
            this.currentPlaybackRate = 5; // 5ms = 200 drops/sec
        } else if (queueLength > 20) {
            // Moderate queue - speed up
            this.currentPlaybackRate = 10; // 10ms = 100 drops/sec
        } else if (queueLength < 5) {
            // Queue getting empty - slow down to preserve traces
            this.currentPlaybackRate = this.basePlaybackRate * 2;
        } else {
            // Normal queue - base rate
            this.currentPlaybackRate = this.basePlaybackRate;
        }
    }

    startConstantRain() {
        // Constant rain timer - plays buffered traces at steady rate
        const playNextTrace = () => {
            if (this.traceQueue.length > 0) {
                const trace = this.traceQueue.shift();
                this.createRaindrop(trace);
            }
            
            // Schedule next raindrop
            setTimeout(playNextTrace, this.currentPlaybackRate);
        };
        
        // Start the constant rain
        playNextTrace();
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