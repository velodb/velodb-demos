import React from 'react';
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Activity, Loader2 } from "lucide-react";
import { useGeneratorStatus } from '@/hooks/api/useGenerator';
import { useRecentClickstream } from '@/hooks/api/useClickstream';

interface ClickstreamPopoutProps {
    partnerId?: number;
}

export const ClickstreamPopout: React.FC<ClickstreamPopoutProps> = ({ partnerId = 44 }) => {
    // Fetch generator status (polls every 3 seconds for rate info)
    const { data: generatorStatus, isLoading: isLoadingGenerator } = useGeneratorStatus();

    // Fetch recent clickstream events (polls every 2 seconds for real-time updates)
    const { data: recentEvents, isLoading: isLoadingEvents } = useRecentClickstream(partnerId, 1);

    // Extract ingestion rate from generator status
    const ingestionRate = generatorStatus?.status.clickstream.current_rate || 0;
    const isSpike = generatorStatus?.status.clickstream.spike_active || false;

    // Calculate progress bar value
    const progressValue = isSpike ? 100 : Math.min((ingestionRate / 2000) * 100, 100);

    // Get the latest event for JSON display
    const latestEvent = recentEvents?.[0];

    // Parse event properties if it's a JSON string
    const parsedEventProps = React.useMemo(() => {
        if (!latestEvent) return null;
        try {
            const props = typeof latestEvent.event_properties === 'string'
                ? JSON.parse(latestEvent.event_properties)
                : latestEvent.event_properties;
            return {
                event_id: latestEvent.event_id,
                event_type: latestEvent.event_type,
                user_id: latestEvent.user_id,
                timestamp: latestEvent.event_timestamp,
                ...props,
            };
        } catch {
            return {
                event_id: latestEvent.event_id,
                event_type: latestEvent.event_type,
                user_id: latestEvent.user_id,
                timestamp: latestEvent.event_timestamp,
                properties: latestEvent.event_properties,
            };
        }
    }, [latestEvent]);

    const isLoading = isLoadingGenerator && isLoadingEvents;

    if (isLoading) {
        return (
            <Card className="w-full max-w-2xl shadow-2xl border-t-4 border-t-[#A855F7] h-[500px]">
                <CardContent className="p-6 flex items-center justify-center h-full">
                    <Loader2 className="w-8 h-8 animate-spin text-purple-500" />
                </CardContent>
            </Card>
        );
    }

    return (
        <Card className="w-full max-w-2xl shadow-2xl border-t-4 border-t-[#A855F7] h-[500px] flex flex-col">
            <CardHeader className="bg-slate-50/50 border-b pb-4 flex-shrink-0">
                <CardTitle className="text-xl font-bold tracking-tight flex items-center gap-3 text-[#A855F7]">
                    <div className="p-2 bg-purple-50 rounded-lg border border-purple-100">
                        <span className="text-2xl">🌊</span>
                    </div>
                    <div>
                        <div className="text-sm font-medium text-slate-500 mb-0.5">EVENT STREAMING</div>
                        CLICKSTREAM :: TOPIC 'CLICKSTREAM_V1'
                    </div>
                </CardTitle>
            </CardHeader>
            <CardContent className="p-6 space-y-6 flex-1 overflow-hidden flex flex-col">

                {/* Ingestion Rate */}
                <div className="space-y-3 bg-purple-50/30 p-4 rounded-xl border border-purple-100 flex-shrink-0">
                    <div className="flex justify-between items-end">
                        <h3 className="text-sm font-bold text-purple-900 flex items-center gap-2">
                            <Activity className="w-4 h-4" />
                            INGESTION RATE
                        </h3>
                        <div className="text-right">
                            <span className={`text-3xl font-black font-mono tracking-tight ${isSpike ? 'text-red-600 animate-pulse' : 'text-slate-800'}`}>
                                {ingestionRate.toLocaleString()}
                            </span>
                            <span className="text-xs font-bold text-slate-400 ml-1 uppercase">msg/sec</span>
                        </div>
                    </div>
                    <div className="relative pt-2">
                        <div className="h-4 w-full bg-slate-200 rounded-full overflow-hidden shadow-inner">
                            <div
                                className={`h-full transition-all duration-300 ease-out ${isSpike ? 'bg-red-500' : 'bg-gradient-to-r from-purple-500 to-pink-500'}`}
                                style={{ width: `${progressValue}%` }}
                            />
                        </div>
                        {isSpike && (
                            <Badge variant="destructive" className="absolute -top-10 right-0 animate-bounce shadow-lg border-2 border-white">
                                TRAFFIC SPIKE DETECTED
                            </Badge>
                        )}
                    </div>
                    <div className="flex justify-between text-[10px] font-mono text-slate-400 uppercase tracking-wider">
                        <span>0 msg/s</span>
                        <span>1k msg/s</span>
                        <span>2k+ msg/s</span>
                    </div>
                </div>

                {/* JSON Inspector - Fixed height to prevent layout shifts */}
                <div className="space-y-2 flex-1 flex flex-col min-h-0">
                    <div className="flex justify-between items-center flex-shrink-0">
                        <h3 className="text-sm font-bold text-slate-700 flex items-center gap-2">
                            <span className="w-1 h-4 bg-[#A855F7] rounded-full"></span>
                            LIVE JSON INSPECTOR (Schema-on-Read)
                        </h3>
                        <div className="flex items-center gap-2">
                            <span className="relative flex h-2 w-2">
                                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75"></span>
                                <span className="relative inline-flex rounded-full h-2 w-2 bg-green-500"></span>
                            </span>
                            <Badge variant="outline" className="font-mono text-[10px] bg-white text-slate-500 border-slate-200 shadow-sm">JSON</Badge>
                        </div>
                    </div>
                    <div className="bg-white text-slate-800 p-4 rounded-lg font-mono text-xs shadow-inner border border-slate-200 relative group flex-1 overflow-auto">
                        {parsedEventProps ? (
                            <pre className="leading-relaxed">
                                {JSON.stringify(parsedEventProps, null, 2).split('\n').map((line, i) => {
                                    const match = line.match(/^(\s*)"([^"]+)": (.+),?$/);
                                    if (match) {
                                        const [, indent, key, value] = match;
                                        let valueColor = 'text-blue-600'; // number/boolean
                                        if (value.startsWith('"')) valueColor = 'text-green-600'; // string
                                        if (value === 'null') valueColor = 'text-slate-400'; // null
                                        return (
                                            <div key={i}>
                                                {indent}<span className="text-purple-700 font-semibold">"{key}"</span>: <span className={valueColor}>{value}</span>
                                            </div>
                                        );
                                    }
                                    return <div key={i} className="text-slate-500">{line}</div>;
                                })}
                            </pre>
                        ) : (
                            <div className="text-slate-400 text-center py-8 italic">
                                Waiting for events...
                            </div>
                        )}
                    </div>
                </div>

            </CardContent>
        </Card>
    );
};
