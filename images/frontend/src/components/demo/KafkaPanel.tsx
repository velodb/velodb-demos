import React, { useState, useEffect, useMemo } from "react";
import { Button } from "@/components/ui/button";
import { Activity, Loader2, RefreshCw } from "lucide-react";

interface KafkaPanelProps {
  partnerId: number;
  onClose: () => void;
}

interface ClickstreamEvent {
  event_id: string;
  event_type: string;
  user_id: number;
  event_properties: string;
  event_timestamp: string;
}

export const KafkaPanel: React.FC<KafkaPanelProps> = ({ partnerId, onClose }) => {
  const [events, setEvents] = useState<ClickstreamEvent[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [messageCount, setMessageCount] = useState(0);

  // Fetch recent events and poll for updates
  useEffect(() => {
    const fetchEvents = async () => {
      try {
        const res = await fetch(
          `/api/clickstream/recent?partner_id=${partnerId}&limit=10`
        );
        if (res.ok) {
          const data = await res.json();
          setEvents(data.events || []);
          setMessageCount((prev) => prev + (data.events?.length || 0));
        }
      } catch (error) {
        console.error("Failed to fetch clickstream events:", error);
      } finally {
        setIsLoading(false);
      }
    };

    fetchEvents();
    const interval = setInterval(fetchEvents, 1000); // Poll every second for real-time feel

    return () => clearInterval(interval);
  }, [partnerId]);

  // Parse event to full JSON
  const parseEvent = (event: ClickstreamEvent) => {
    try {
      const props =
        typeof event.event_properties === "string"
          ? JSON.parse(event.event_properties)
          : event.event_properties;
      return {
        event_id: event.event_id,
        event_type: event.event_type,
        user_id: event.user_id,
        timestamp: event.event_timestamp,
        ...props,
      };
    } catch {
      return {
        event_id: event.event_id,
        event_type: event.event_type,
        user_id: event.user_id,
        timestamp: event.event_timestamp,
        properties: event.event_properties,
      };
    }
  };

  // Syntax highlight JSON
  const highlightJson = (json: string) => {
    return json.split("\n").map((line, i) => {
      const match = line.match(/^(\s*)"([^"]+)": (.+?),?$/);
      if (match) {
        const [, indent, key, value] = match;
        let valueColor = "text-blue-600"; // number/boolean
        if (value.startsWith('"')) valueColor = "text-green-600"; // string
        if (value === "null") valueColor = "text-slate-400"; // null
        return (
          <div key={i}>
            {indent}
            <span className="text-orange-600 font-semibold">"{key}"</span>:{" "}
            <span className={valueColor}>{value}</span>
          </div>
        );
      }
      return (
        <div key={i} className="text-slate-500">
          {line}
        </div>
      );
    });
  };

  const latestEvent = events[0];
  const parsedLatestEvent = latestEvent ? parseEvent(latestEvent) : null;

  return (
    <div
      className="h-full bg-white rounded-lg border border-gray-200 p-6 flex flex-col overflow-hidden"
      data-testid="kafka-panel"
    >
      {/* Header */}
      <div className="flex items-center justify-between mb-4 flex-shrink-0">
        <h2 className="text-xl font-semibold flex items-center gap-2">
          <Activity className="h-5 w-5 text-orange-500" />
          Kafka Stream Viewer
        </h2>
        <Button variant="ghost" size="sm" onClick={onClose}>
          ✕
        </Button>
      </div>

      {isLoading ? (
        <div className="flex-1 flex items-center justify-center">
          <Loader2 className="h-8 w-8 animate-spin text-orange-500" />
        </div>
      ) : (
        <div className="flex-1 flex flex-col gap-4 overflow-y-auto min-h-0">
          {/* Topic Info */}
          <div className="bg-orange-50 rounded-lg p-4 border border-orange-200 flex-shrink-0">
            <div className="flex justify-between items-center">
              <div>
                <div className="text-xs text-orange-600 uppercase tracking-wide font-medium mb-1">
                  Topic
                </div>
                <div className="font-mono text-lg font-bold text-gray-900">
                  clickstream-events
                </div>
              </div>
              <div className="text-right">
                <div className="text-xs text-orange-600 uppercase tracking-wide font-medium mb-1">
                  Messages/sec
                </div>
                <div className="text-2xl font-bold text-orange-600 tabular-nums flex items-center gap-2">
                  ~100
                  <span className="relative flex h-2 w-2">
                    <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75"></span>
                    <span className="relative inline-flex rounded-full h-2 w-2 bg-green-500"></span>
                  </span>
                </div>
              </div>
            </div>
          </div>

          {/* Live JSON Viewer */}
          <div className="flex flex-col flex-shrink-0">
            <div className="flex justify-between items-center mb-2">
              <h3 className="text-sm font-semibold text-gray-700 flex items-center gap-2">
                <span className="w-1 h-4 bg-orange-500 rounded-full"></span>
                Live Message Stream
              </h3>
              <div className="flex items-center gap-2 text-xs text-gray-500">
                <RefreshCw className="h-3 w-3 animate-spin" />
                Auto-refreshing
              </div>
            </div>

            <div className="bg-slate-900 text-slate-100 rounded-lg p-4 font-mono text-xs overflow-auto" style={{ minHeight: '180px', maxHeight: '280px' }}>
              {parsedLatestEvent ? (
                <pre className="leading-relaxed whitespace-pre-wrap text-green-400">
                  {JSON.stringify(parsedLatestEvent, null, 2)}
                </pre>
              ) : (
                <div className="text-slate-400 text-center py-8 italic">
                  Waiting for messages...
                </div>
              )}
            </div>
          </div>

          {/* Recent Messages List */}
          <div className="flex-shrink-0">
            <h3 className="text-sm font-semibold text-gray-700 mb-2 flex items-center gap-2">
              <span className="w-1 h-4 bg-orange-500 rounded-full"></span>
              Recent Messages ({events.length})
            </h3>
            <div className="bg-gray-50 rounded-lg border border-gray-200 overflow-auto" style={{ maxHeight: '200px' }}>
              {events.map((event, idx) => {
                // Parse event properties to get product name
                let productName = "";
                try {
                  const props = typeof event.event_properties === "string"
                    ? JSON.parse(event.event_properties)
                    : event.event_properties;
                  productName = props.product_name || "";
                } catch {}

                return (
                  <div
                    key={event.event_id}
                    className={`px-3 py-2 text-xs flex items-center gap-3 ${
                      idx !== 0 ? "border-t border-gray-200" : ""
                    } ${idx === 0 ? "bg-orange-50" : ""}`}
                  >
                    <span
                      className={`px-2 py-0.5 rounded text-xs font-medium flex-shrink-0 ${
                        event.event_type === "purchase"
                          ? "bg-green-100 text-green-700"
                          : event.event_type === "cart"
                          ? "bg-blue-100 text-blue-700"
                          : event.event_type === "view"
                          ? "bg-purple-100 text-purple-700"
                          : "bg-gray-100 text-gray-700"
                      }`}
                    >
                      {event.event_type}
                    </span>
                    <span className="text-gray-800 truncate flex-1 font-medium" data-testid="product-name">
                      {productName || "Unknown product"}
                    </span>
                    <span className="text-gray-400 flex-shrink-0">
                      user_{event.user_id}
                    </span>
                  </div>
                );
              })}
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
