export class TelemetryAnalyzer {
    constructor() {
        this.lastTraceCount = 0;
        this.lastLogCount = 0;
        this.traceStartTimes = new Map();
        this.traceDurations = [];
    }

    analyzeTelemetry(rawTelemetry) {
        const traces = this.analyzeTraces(rawTelemetry);
        const metrics = this.analyzeMetrics(rawTelemetry);
        const logs = this.analyzeLogs(rawTelemetry);

        return { traces, metrics, logs };
    }

    analyzeTraces(rawTelemetry) {
        if (!rawTelemetry.resourceSpans) {
            return { count: 0, errorRate: 0, averageLength: 0, traceIds: [] };
        }

        let totalTraces = 0;
        let errorTraces = 0;
        let totalDuration = 0;
        let traceCount = 0;
        const traceIds = [];

        rawTelemetry.resourceSpans.forEach((resourceSpan) => {
            resourceSpan.scopeSpans?.forEach((scopeSpan) => {
                scopeSpan.spans?.forEach((span) => {
                    totalTraces++;
                    
                    // Collect trace IDs with their error status
                    if (span.traceId) {
                        const isError = span.status?.code === 2;
                        traceIds.push({
                            id: span.traceId,
                            shortId: span.traceId.substring(0, 8), // First 8 characters
                            isError: isError
                        });
                    }
                    
                    if (span.kind === 1) {
                        this.traceStartTimes.set(span.traceId, Date.now());
                    }
                    
                    if (span.endTimeUnixNano && span.startTimeUnixNano) {
                        const duration = (span.endTimeUnixNano - span.startTimeUnixNano) / 1000000;
                        this.traceDurations.push(duration);
                        totalDuration += duration;
                        traceCount++;
                        
                        this.traceStartTimes.delete(span.traceId);
                    }
                    
                    if (span.status?.code === 2) {
                        errorTraces++;
                    }
                });
            });
        });

        const errorRate = totalTraces > 0 ? errorTraces / totalTraces : 0;
        const averageLength = traceCount > 0 ? totalDuration / traceCount : 0;
        
        if (this.traceDurations.length > 100) {
            this.traceDurations = this.traceDurations.slice(-100);
        }

        return {
            count: totalTraces,
            errorRate,
            averageLength,
            traceIds: traceIds
        };
    }

    analyzeMetrics(rawTelemetry) {
        if (!rawTelemetry.resourceMetrics) {
            return { cpu: 0, memory: 0, disk: 0, critical: false };
        }

        let cpu = 0;
        let memory = 0;
        let disk = 0;
        let critical = false;

        rawTelemetry.resourceMetrics.forEach((resourceMetric) => {
            resourceMetric.scopeMetrics?.forEach((scopeMetric) => {
                scopeMetric.metrics?.forEach((metric) => {
                    // Handle CPU utilization
                    if (metric.name === 'system.cpu.utilization' && metric.gauge) {
                        metric.gauge.dataPoints?.forEach((point) => {
                            if (point.asDouble !== undefined) {
                                cpu = Math.max(cpu, point.asDouble * 100);
                            }
                        });
                    }
                    
                    // Handle memory utilization  
                    if (metric.name === 'system.memory.utilization' && metric.gauge) {
                        metric.gauge.dataPoints?.forEach((point) => {
                            if (point.asDouble !== undefined) {
                                memory = Math.max(memory, point.asDouble * 100);
                            }
                        });
                    }
                    
                    // Handle disk I/O
                    if (metric.name === 'system.disk.io' && metric.sum) {
                        metric.sum.dataPoints?.forEach((point) => {
                            if (point.asInt !== undefined) {
                                // Normalize disk I/O to a percentage (assuming high activity > 10000)
                                disk = Math.max(disk, Math.min(100, parseInt(point.asInt) / 100));
                            }
                        });
                    }
                });
            });
        });

        critical = cpu > 80 || memory > 80 || disk > 80;

        return { cpu, memory, disk, critical };
    }

    analyzeLogs(rawTelemetry) {
        if (!rawTelemetry.resourceLogs) {
            return { errorRate: 0, totalCount: 0 };
        }

        let totalLogs = 0;
        let errorLogs = 0;

        rawTelemetry.resourceLogs.forEach((resourceLog) => {
            resourceLog.scopeLogs?.forEach((scopeLog) => {
                scopeLog.logRecords?.forEach((log) => {
                    totalLogs++;
                    
                    if (log.severityText === 'ERROR' || log.severityText === 'FATAL' || 
                        (log.severityNumber && log.severityNumber >= 17)) {
                        errorLogs++;
                    }
                });
            });
        });

        const errorRate = totalLogs > 0 ? errorLogs / totalLogs : 0;

        return { errorRate, totalCount: totalLogs };
    }


}