import React, { useMemo } from 'react';
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import ReactECharts from 'echarts-for-react';
import { QueryTooltip } from './QueryTooltip';
import { useRevenueMetrics, useFunnel, useTopProducts } from '@/hooks/api/useAnalytics';
import { Skeleton } from "@/components/ui/skeleton";
import { AlertCircle } from 'lucide-react';

interface MetricBoxProps {
    label: string;
    value: string | number;
    highlight?: boolean;
    variant?: 'default' | 'destructive';
    loading?: boolean;
}

const MetricBox: React.FC<MetricBoxProps> = ({ label, value, highlight, variant = 'default', loading }) => (
    <div className={`p-4 rounded-lg border transition-all duration-200 hover:shadow-md ${highlight ? 'bg-sky-50 border-sky-200' : 'bg-white border-slate-200'} ${variant === 'destructive' ? 'bg-red-50 border-red-200' : ''}`}>
        <div className="text-xs text-slate-500 uppercase tracking-wider mb-1 font-semibold">{label}</div>
        {loading ? (
            <Skeleton className="h-8 w-32" />
        ) : (
            <div className={`text-2xl font-black tracking-tight ${highlight ? 'text-sky-600' : 'text-slate-900'} ${variant === 'destructive' ? 'text-red-600' : ''}`}>
                {value}
            </div>
        )}
    </div>
);

interface VeloDBPopoutProps {
    partnerId: number;
    onRefreshMVs?: () => void;
}

export const VeloDBPopout: React.FC<VeloDBPopoutProps> = ({
    partnerId,
    onRefreshMVs,
}) => {
    // Fetch data from backend API
    const { data: revenueData, isLoading: isLoadingRevenue, error: revenueError } = useRevenueMetrics(partnerId, 24);
    const { data: funnelData, isLoading: isLoadingFunnel, error: funnelError } = useFunnel(partnerId, 7);
    const { data: productsData, isLoading: isLoadingProducts, error: productsError } = useTopProducts(partnerId, 10);

    // Transform API data for display
    const revenueMetrics = useMemo(() => {
        if (!revenueData || revenueData.metrics.length === 0) {
            return { currentHr: '$0', rolling24h: '$0' };
        }

        // Get current hour revenue (last metric)
        const currentMetric = revenueData.metrics[revenueData.metrics.length - 1];
        const currentHr = `$${currentMetric.revenue.toLocaleString('en-US', { maximumFractionDigits: 0 })}`;

        // Calculate rolling 24h revenue
        const rolling24h = revenueData.metrics.reduce((sum, m) => sum + m.revenue, 0);
        const rolling24hStr = `$${rolling24h.toLocaleString('en-US', { maximumFractionDigits: 0 })}`;

        return { currentHr, rolling24h: rolling24hStr };
    }, [revenueData]);

    // Mock SLA metrics (backend doesn't provide this yet)
    const slaMetrics = { p95: 145, breaches: 0 };

    // Transform funnel data for chart
    const funnelChartData = useMemo(() => {
        if (!funnelData) {
            return [];
        }

        return [
            { step: 'VISITORS', pct: 100 },
            { step: 'ADD2CART', pct: (funnelData.view_to_cart_rate * 100).toFixed(1) },
            { step: 'CHECKOUT', pct: (funnelData.view_to_cart_rate * funnelData.cart_to_purchase_rate * 100).toFixed(1) },
        ];
    }, [funnelData]);

    // Transform product data for chart - show revenue instead of growth (which requires historical data)
    const productTrends = useMemo(() => {
        if (!productsData) {
            return [];
        }

        return productsData.products.map(p => ({
            product: p.name.length > 12 ? p.name.substring(0, 12) + '...' : p.name,
            category: 'Electronics', // Backend doesn't provide category yet
            revenue: p.revenue,
            revenueFormatted: `$${p.revenue.toLocaleString('en-US', { maximumFractionDigits: 0 })}`,
            rank: p.rank,
        }));
    }, [productsData]);

    // Show error state if any query failed
    if (revenueError || funnelError || productsError) {
        return (
            <Card className="w-full max-w-3xl shadow-2xl border-t-4 border-t-red-500">
                <CardContent className="p-6">
                    <div className="flex items-center gap-3 text-red-600">
                        <AlertCircle className="w-6 h-6" />
                        <div>
                            <h3 className="font-bold">Failed to load data</h3>
                            <p className="text-sm text-red-500">
                                {revenueError?.message || funnelError?.message || productsError?.message}
                            </p>
                        </div>
                    </div>
                </CardContent>
            </Card>
        );
    }
    // Chart Configs (memoized with real data)
    const revenueOption = useMemo(() => {
        const timeLabels = revenueData?.metrics.map((m) => {
            const date = new Date(m.hour);
            return date.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
        }) || [];

        const revenueValues = revenueData?.metrics.map((m) => m.revenue) || [];

        return {
            grid: { top: 10, right: 10, bottom: 20, left: 50 },
            tooltip: { trigger: 'axis', formatter: (params: any) => {
                const value = params[0].value;
                return `${params[0].axisValue}<br/>Revenue: $${value.toLocaleString()}`;
            }},
            xAxis: { type: 'category', data: timeLabels },
            yAxis: { type: 'value', splitLine: { lineStyle: { type: 'dashed' } } },
            series: [{
                data: revenueValues,
                type: 'line',
                smooth: true,
                areaStyle: {
                    color: {
                        type: 'linear',
                        x: 0, y: 0, x2: 0, y2: 1,
                        colorStops: [{ offset: 0, color: 'rgba(14, 165, 233, 0.5)' }, { offset: 1, color: 'rgba(14, 165, 233, 0)' }]
                    }
                },
                lineStyle: { color: '#0EA5E9', width: 3 },
                itemStyle: { color: '#0EA5E9' }
            }]
        };
    }, [revenueData]);

    const funnelOption = useMemo(() => ({
        tooltip: { trigger: 'item', formatter: '{b} : {c}%' },
        series: [
            {
                name: 'Funnel',
                type: 'funnel',
                left: '10%',
                top: 10,
                bottom: 10,
                width: '80%',
                min: 0,
                max: 100,
                minSize: '0%',
                maxSize: '100%',
                sort: 'descending',
                gap: 2,
                label: { show: true, position: 'inside' },
                labelLine: { length: 10, lineStyle: { width: 1, type: 'solid' } },
                itemStyle: { borderColor: '#fff', borderWidth: 1 },
                emphasis: { label: { fontSize: 20 } },
                data: funnelChartData.map(d => ({ value: parseFloat(d.pct as any), name: d.step }))
            }
        ]
    }), [funnelChartData]);

    const racingBarOption = useMemo(() => ({
        grid: { top: 10, right: 80, bottom: 10, left: 100 },
        xAxis: { max: 'dataMax', show: false },
        yAxis: {
            type: 'category',
            data: productTrends.map(p => p.product),
            inverse: true,
            axisLine: { show: false },
            axisTick: { show: false },
            axisLabel: { fontWeight: 'bold', color: '#64748b' }
        },
        series: [{
            realtimeSort: true,
            seriesLayoutBy: 'column',
            type: 'bar',
            data: productTrends.map(p => p.revenue),
            label: {
                show: true,
                position: 'right',
                valueAnimation: true,
                formatter: (params: any) => `$${params.value.toLocaleString()}`,
                fontWeight: 'bold',
                color: '#0EA5E9'
            },
            itemStyle: {
                color: {
                    type: 'linear',
                    x: 0, y: 0, x2: 1, y2: 0,
                    colorStops: [{ offset: 0, color: '#38BDF8' }, { offset: 1, color: '#0284C7' }]
                },
                borderRadius: [0, 4, 4, 0]
            }
        }],
        animationDuration: 0,
        animationDurationUpdate: 1000,
        animationEasing: 'linear',
        animationEasingUpdate: 'linear'
    }), [productTrends]);

    const slaOption = {
        grid: { top: 10, right: 10, bottom: 20, left: 40 },
        tooltip: { trigger: 'axis' },
        xAxis: { type: 'category', data: ['10:00', '10:01', '10:02', '10:03', '10:04', '10:05'] },
        yAxis: { type: 'value' },
        series: [{
            data: [120, 132, 101, 134, 90, slaMetrics.p95],
            type: 'line',
            smooth: true,
            lineStyle: { color: '#10b981', width: 2 },
            markLine: {
                symbol: ['none', 'none'],
                label: { show: true, position: 'end', formatter: 'SLA (200ms)' },
                data: [{ yAxis: 200, lineStyle: { color: '#ef4444', type: 'dashed' } }]
            }
        }]
    };

    return (
        <Card className="w-full max-w-3xl shadow-2xl border-t-4 border-t-[#0EA5E9]">
            <CardHeader className="bg-slate-50/50 pb-4 border-b">
                <div className="flex justify-between items-center">
                    <CardTitle className="text-xl font-bold tracking-tight flex items-center gap-3 text-[#0EA5E9]">
                        <div className="p-2 bg-sky-50 rounded-lg border border-sky-100">
                            <span className="text-2xl">⚡</span>
                        </div>
                        <div>
                            <div className="text-sm font-medium text-slate-500 mb-0.5">REAL-TIME ANALYTICS</div>
                            VELODB CLOUD :: ANALYTICS WORKSPACE
                        </div>
                    </CardTitle>
                    <Badge variant="secondary" className="font-mono bg-slate-100 text-slate-600 border-slate-200">partner_id: {partnerId}</Badge>
                </div>
            </CardHeader>
            <CardContent className="p-0">
                <Tabs defaultValue="overview" className="w-full">
                    <div className="px-6 pt-2 bg-slate-50/50 border-b">
                        <TabsList className="grid w-full grid-cols-3 mb-4 bg-slate-200/50 p-1">
                            <TabsTrigger value="overview" className="data-[state=active]:bg-white data-[state=active]:text-sky-600 data-[state=active]:shadow-sm font-semibold">OVERVIEW</TabsTrigger>
                            <TabsTrigger value="funnel" className="data-[state=active]:bg-white data-[state=active]:text-sky-600 data-[state=active]:shadow-sm font-semibold">FUNNEL (MV)</TabsTrigger>
                            <TabsTrigger value="product-intel" className="data-[state=active]:bg-white data-[state=active]:text-sky-600 data-[state=active]:shadow-sm font-semibold">PROD INTEL</TabsTrigger>
                        </TabsList>
                    </div>

                    {/* Tab 1: Overview */}
                    <TabsContent value="overview" className="p-6 space-y-8 mt-0 animate-in fade-in-50 slide-in-from-bottom-2">
                        <div>
                            <div className="flex items-center justify-between mb-4">
                                <QueryTooltip query="SELECT sum(amt) OVER (ROWS BETWEEN 23 PRECEDING...) FROM orders_stream" latency="12ms" scanned="1.2GB">
                                    <h3 className="font-bold text-lg text-slate-800 flex items-center gap-2 hover:text-sky-600 transition-colors cursor-help">
                                        <span className="w-1 h-4 bg-sky-500 rounded-full"></span>
                                        Revenue Pulse (Window Function + CDC)
                                    </h3>
                                </QueryTooltip>
                                <Badge variant="outline" className="text-xs bg-green-50 text-green-700 border-green-200">Sub-second Refresh</Badge>
                            </div>

                            <div className="grid grid-cols-2 gap-6 mb-6">
                                <MetricBox label="Current Hr Revenue" value={revenueMetrics.currentHr} loading={isLoadingRevenue} />
                                <MetricBox label="Rolling 24h Revenue" value={revenueMetrics.rolling24h} highlight loading={isLoadingRevenue} />
                            </div>
                            <div className="h-[200px] w-full border rounded-lg p-2 bg-white">
                                <ReactECharts option={revenueOption} style={{ height: '100%', width: '100%' }} />
                            </div>
                        </div>

                        <Separator />

                        <div>
                            <QueryTooltip query="SELECT percentile_cont(0.95) WITHIN GROUP (ORDER BY latency) FROM support_tickets WHERE status='closed'" latency="8ms" scanned="500MB">
                                <h3 className="font-bold text-lg mb-4 text-slate-800 flex items-center gap-2 hover:text-sky-600 transition-colors cursor-help">
                                    <span className="w-1 h-4 bg-sky-500 rounded-full"></span>
                                    Support SLA (JSON Extract)
                                </h3>
                            </QueryTooltip>
                            <div className="grid grid-cols-2 gap-6 mb-6">
                                <MetricBox label="p95 Latency" value={`${slaMetrics.p95} ms`} />
                                <MetricBox
                                    label="SLA Breaches (>2s)"
                                    value={slaMetrics.breaches}
                                    variant={slaMetrics.breaches > 0 ? 'destructive' : 'default'}
                                />
                            </div>
                            <div className="h-[200px] w-full border rounded-lg p-2 bg-white">
                                <ReactECharts option={slaOption} style={{ height: '100%', width: '100%' }} />
                            </div>
                        </div>
                    </TabsContent>

                    {/* Tab 2: Funnel */}
                    <TabsContent value="funnel" className="p-6 mt-0 animate-in fade-in-50 slide-in-from-bottom-2">
                        <div className="flex justify-between items-center mb-6">
                            <div className="flex flex-col">
                                <QueryTooltip query={`CREATE MATERIALIZED VIEW mv_realtime_funnel AS
SELECT step, count(*) as count, 
       count(*) * 100.0 / (SELECT count(*) FROM funnel WHERE step='VISITORS') as pct
FROM funnel_events
GROUP BY step;`} type="SQL" latency="N/A (Pre-computed)">
                                    <span className="font-mono text-sm font-bold text-slate-700 hover:text-sky-600 transition-colors cursor-help">SOURCE: mv_realtime_funnel</span>
                                </QueryTooltip>
                                <span className="text-xs text-slate-500">Materialized View</span>
                            </div>
                            <Button size="sm" onClick={onRefreshMVs} className="gap-2 bg-sky-600 hover:bg-sky-700 text-white shadow-md shadow-sky-200">
                                🔄 REFRESH MVs
                            </Button>
                        </div>

                        <div className="h-[400px] w-full border rounded-lg p-4 bg-white shadow-sm">
                            <ReactECharts option={funnelOption} style={{ height: '100%', width: '100%' }} />
                        </div>
                    </TabsContent>

                    {/* Tab 3: Product Intel */}
                    <TabsContent value="product-intel" className="p-6 mt-0 animate-in fade-in-50 slide-in-from-bottom-2">
                        <div className="mb-6">
                            <QueryTooltip query="WITH product_metrics AS (SELECT ... json_extract(c.props,'$.cart_val')...) FROM clickstream JOIN orders ON..." latency="450ms" scanned="45M rows">
                                <h3 className="font-bold mb-3 text-slate-800 hover:text-sky-600 transition-colors cursor-help">Complex Query Execution</h3>
                            </QueryTooltip>
                            <div className="bg-slate-900 text-slate-300 p-3 rounded-lg font-mono text-xs overflow-x-auto border border-slate-800 shadow-inner hover:border-sky-500/50 transition-colors">
                                WITH product_metrics AS (SELECT ... json_extract(c.props,'$.cart_val')...)
                            </div>
                        </div>

                        <div className="h-[400px] w-full border rounded-lg p-4 bg-white shadow-sm">
                            <h4 className="text-sm font-bold text-slate-500 uppercase tracking-wider mb-4 text-center">Top Products by Revenue (7-Day)</h4>
                            <ReactECharts option={racingBarOption} style={{ height: '100%', width: '100%' }} />
                        </div>
                        <div className="text-xs text-slate-400 mt-3 text-right font-mono">
                            Execution Time: 0.45s (Scanned 45M rows)
                        </div>
                    </TabsContent>
                </Tabs>
            </CardContent>
        </Card>
    );
};
