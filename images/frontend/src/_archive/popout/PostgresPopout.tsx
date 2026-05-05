import React, { useState, useEffect, useRef } from 'react';
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Package, Truck, Trash2, Database, ShoppingCart, RefreshCw, MapPin, Plus } from "lucide-react";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { useEcommerceOrders, useUpdateEcommerceOrderStatus, useDeleteEcommerceOrder, useCreateEcommerceOrder } from '@/hooks/api/useEcommerceOrders';
import { useToast } from "@/hooks/use-toast";
import type { EcommerceOrder } from '@/types/api';

interface PostgresPopoutProps {
    partnerId: number;
    onOrderCreated?: (orderId: number) => void;
}

interface CDCLogEntry {
    id: string;
    time: string;
    op: 'INSERT' | 'UPDATE' | 'DELETE';
    table: string;
    lsn: string;
    details: string;
}

export const PostgresPopout: React.FC<PostgresPopoutProps> = ({
    partnerId,
    onOrderCreated,
}) => {
    const [cdcLog, setCdcLog] = useState<CDCLogEntry[]>([]);
    const prevOrderIdsRef = useRef<Set<number>>(new Set());
    const scrollRef = useRef<HTMLDivElement>(null);
    const { toast } = useToast();

    // Fetch recent ecommerce orders with real-time polling (every 3 seconds)
    const { data: recentOrders, isLoading } = useEcommerceOrders(10);

    // Use ecommerce API mutations
    const updateStatusMutation = useUpdateEcommerceOrderStatus();
    const deleteOrderMutation = useDeleteEcommerceOrder();
    const createOrderMutation = useCreateEcommerceOrder();

    // Detect new orders and generate CDC logs
    useEffect(() => {
        if (!recentOrders) return;

        const currentIds = new Set(recentOrders.map(o => o.order_id));
        const prevIds = prevOrderIdsRef.current;

        // Find new orders (not in previous set)
        const newOrders = recentOrders.filter(o => !prevIds.has(o.order_id));

        if (newOrders.length > 0 && prevIds.size > 0) {
            const now = new Date().toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' }) + '.' + Math.floor(Math.random() * 999);

            const newLogs: CDCLogEntry[] = newOrders.map(order => {
                const productNames = order.products.map(p => p.product_name).join(', ');
                return {
                    id: Math.random().toString(36).substr(2, 9),
                    time: now,
                    op: 'INSERT' as const,
                    table: 'kibana_sample_data_ecommerce',
                    lsn: `0/${Math.floor(Math.random() * 1000000).toString(16).toUpperCase()}`,
                    details: `id=${order.order_id}, customer="${order.customer_full_name}", products="${productNames}", amt=$${order.taxful_total_price.toFixed(2)}`,
                };
            });

            setCdcLog(prev => [...newLogs, ...prev].slice(0, 50));
        }

        prevOrderIdsRef.current = currentIds;
    }, [recentOrders]);

    // State for selected order (for Ship/Refund actions)
    const [selectedOrderId, setSelectedOrderId] = useState<number | null>(null);

    // State for refund confirmation dialog
    const [orderToRefund, setOrderToRefund] = useState<EcommerceOrder | null>(null);

    // Handle shipping an order
    const handleShipOrder = async (order: EcommerceOrder) => {
        try {
            const result = await updateStatusMutation.mutateAsync({
                orderId: order.order_id,
                status: 'shipped',
            });

            toast({
                title: "Order Shipped!",
                description: `Order #${order.order_id} status changed to SHIPPED`,
            });

            // Add CDC log entry for UPDATE
            const now = new Date().toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' }) + '.' + Math.floor(Math.random() * 999);
            const newLog: CDCLogEntry = {
                id: Math.random().toString(36).substr(2, 9),
                time: now,
                op: 'UPDATE',
                table: 'kibana_sample_data_ecommerce',
                lsn: `0/${Math.floor(Math.random() * 1000000).toString(16).toUpperCase()}`,
                details: `id=${order.order_id}, status="${result.old_status}" → "${result.new_status}"`,
            };
            setCdcLog((prevLogs) => [newLog, ...prevLogs].slice(0, 50));
        } catch (error) {
            toast({
                title: "Ship Failed",
                description: error instanceof Error ? error.message : "Failed to ship order",
                variant: "destructive",
            });
        }
    };

    // Handle refunding (deleting) an order
    const handleRefundOrder = async (order: EcommerceOrder) => {
        try {
            await deleteOrderMutation.mutateAsync(order.order_id);

            toast({
                title: "Order Refunded!",
                description: `Order #${order.order_id} has been deleted`,
            });

            // Notify parent if callback provided
            if (onOrderCreated) {
                onOrderCreated(order.order_id);
            }

            // Add CDC log entry for DELETE
            const now = new Date().toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' }) + '.' + Math.floor(Math.random() * 999);
            const newLog: CDCLogEntry = {
                id: Math.random().toString(36).substr(2, 9),
                time: now,
                op: 'DELETE',
                table: 'kibana_sample_data_ecommerce',
                lsn: `0/${Math.floor(Math.random() * 1000000).toString(16).toUpperCase()}`,
                details: `id=${order.order_id}, customer="${order.customer_full_name}" DELETED`,
            };
            setCdcLog((prevLogs) => [newLog, ...prevLogs].slice(0, 50));
        } catch (error) {
            toast({
                title: "Refund Failed",
                description: error instanceof Error ? error.message : "Failed to refund order",
                variant: "destructive",
            });
        }
    };

    // Handle creating a new order
    const handleCreateOrder = async () => {
        try {
            const result = await createOrderMutation.mutateAsync();

            toast({
                title: "Order Created!",
                description: `Order #${result.order_id} created for ${result.customer_name}`,
            });

            // Notify parent if callback provided
            if (onOrderCreated) {
                onOrderCreated(result.order_id);
            }

            // Add CDC log entry for INSERT
            const now = new Date().toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' }) + '.' + Math.floor(Math.random() * 999);
            const productNames = result.products.map(p => p.product_name).join(', ');
            const newLog: CDCLogEntry = {
                id: Math.random().toString(36).substr(2, 9),
                time: now,
                op: 'INSERT',
                table: 'kibana_sample_data_ecommerce',
                lsn: `0/${Math.floor(Math.random() * 1000000).toString(16).toUpperCase()}`,
                details: `id=${result.order_id}, customer="${result.customer_name}", products="${productNames}", amt=$${result.total_price.toFixed(2)}`,
            };
            setCdcLog((prevLogs) => [newLog, ...prevLogs].slice(0, 50));
        } catch (error) {
            toast({
                title: "Create Order Failed",
                description: error instanceof Error ? error.message : "Failed to create order",
                variant: "destructive",
            });
        }
    };

    // Auto-scroll CDC log
    useEffect(() => {
        if (scrollRef.current) {
            scrollRef.current.scrollTop = 0;
        }
    }, [cdcLog]);

    // Get gender-based emoji avatar
    const getAvatar = (gender: 'MALE' | 'FEMALE') => {
        return gender === 'FEMALE' ? '👩' : '👨';
    };

    // Format product names for display (truncate if too long)
    const formatProducts = (order: EcommerceOrder) => {
        const names = order.products.map(p => p.product_name);
        if (names.length <= 2) {
            return names.join(', ');
        }
        return `${names[0]}, ${names[1]} +${names.length - 2} more`;
    };

    return (
        <Card className="w-full max-w-5xl shadow-2xl border-t-4 border-t-[#336791] flex flex-col md:flex-row overflow-hidden h-[600px]">

            {/* LEFT PANEL: Retail Ops Dashboard */}
            <div className="flex-1 flex flex-col border-r border-slate-200 bg-slate-50/30">
                <CardHeader className="bg-white border-b pb-4 flex-shrink-0">
                    <div className="flex justify-between items-start">
                        <CardTitle className="text-xl font-bold tracking-tight flex items-center gap-3 text-slate-800">
                            <div className="p-2 bg-blue-600 rounded-lg shadow-sm text-white">
                                <ShoppingCart className="w-5 h-5" />
                            </div>
                            <div>
                                <div className="text-xs font-bold text-blue-600 uppercase tracking-wider mb-0.5">Retail Ops</div>
                                Order Manager
                            </div>
                        </CardTitle>
                        <Badge variant="outline" className="gap-1.5 bg-blue-50 text-blue-700 border-blue-200">
                            <img src="/postgres-logo.png" alt="PG" className="w-3 h-3" />
                            Powered by PostgreSQL
                        </Badge>
                    </div>
                </CardHeader>

                <CardContent className="p-0 flex-1 flex flex-col overflow-hidden">
                    {/* Info Banner with Create Order Button */}
                    <div className="p-3 bg-gradient-to-r from-blue-50 to-indigo-50 border-b border-blue-100 flex-shrink-0 flex items-center justify-between gap-3">
                        <p className="text-xs text-blue-700 flex-1">
                            <span className="font-semibold">Real-time ecommerce orders</span> with actual product names. Click an order to Ship or Refund.
                        </p>
                        <Button
                            size="sm"
                            onClick={handleCreateOrder}
                            disabled={createOrderMutation.isPending}
                            className="h-7 px-3 text-xs bg-green-600 hover:bg-green-700 text-white shadow-sm"
                            data-testid="create-order-btn"
                        >
                            {createOrderMutation.isPending ? (
                                <RefreshCw className="w-3 h-3 mr-1 animate-spin" />
                            ) : (
                                <Plus className="w-3 h-3 mr-1" />
                            )}
                            Create Order
                        </Button>
                    </div>

                    {/* Orders List */}
                    <div className="flex-1 overflow-auto p-4">
                        <h3 className="text-xs font-bold text-slate-500 uppercase tracking-wider mb-3 px-1 flex items-center gap-2">
                            Recent Ecommerce Orders
                            {!isLoading && (
                                <span className="relative flex h-2 w-2">
                                    <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75"></span>
                                    <span className="relative inline-flex rounded-full h-2 w-2 bg-green-500"></span>
                                </span>
                            )}
                        </h3>
                        <div className="space-y-3">
                            {recentOrders?.map((order) => {
                                const isSelected = selectedOrderId === order.order_id;
                                const status = order.order_status.toUpperCase();
                                return (
                                    <div
                                        key={order.order_id}
                                        onClick={() => setSelectedOrderId(isSelected ? null : order.order_id)}
                                        className={`bg-white p-3 rounded-xl border shadow-sm cursor-pointer transition-all animate-in slide-in-from-left-2 duration-300 ${
                                            isSelected ? 'border-blue-500 ring-2 ring-blue-200' : 'border-slate-200 hover:border-blue-300'
                                        }`}
                                    >
                                        <div className="flex items-center justify-between">
                                            <div className="flex items-center gap-3">
                                                <div className="w-10 h-10 rounded-full bg-slate-100 flex items-center justify-center text-xl border border-slate-200">
                                                    {getAvatar(order.customer_gender)}
                                                </div>
                                                <div>
                                                    <div className="font-bold text-slate-800 text-sm">{order.customer_full_name}</div>
                                                    <div className="text-xs text-slate-500 flex items-center gap-1">
                                                        <span className="font-mono text-slate-400">#{order.order_id}</span>
                                                        <span>•</span>
                                                        <MapPin className="w-3 h-3" />
                                                        <span>{order.geoip.city_name}</span>
                                                    </div>
                                                </div>
                                            </div>
                                            <div className="text-right">
                                                <div className="font-bold text-slate-900 font-mono text-sm">${order.taxful_total_price.toFixed(2)}</div>
                                                <Badge
                                                    variant={status === 'SHIPPED' || status === 'DELIVERED' ? 'default' : status === 'CANCELLED' ? 'destructive' : 'secondary'}
                                                    className={`text-[10px] px-1.5 py-0 h-5 ${status === 'SHIPPED' || status === 'DELIVERED' ? 'bg-green-100 text-green-700 hover:bg-green-200' : ''}`}
                                                >
                                                    {status}
                                                </Badge>
                                            </div>
                                        </div>
                                        {/* Product names */}
                                        <div className="mt-2 text-xs text-slate-600 bg-slate-50 px-2 py-1.5 rounded-lg">
                                            <span className="font-medium text-slate-500">Products: </span>
                                            {formatProducts(order)}
                                        </div>
                                        {/* Action buttons when selected */}
                                        {isSelected && (
                                            <div className="mt-3 flex gap-2 pt-2 border-t border-slate-100">
                                                <Button
                                                    size="sm"
                                                    variant="outline"
                                                    onClick={(e) => { e.stopPropagation(); handleShipOrder(order); }}
                                                    disabled={updateStatusMutation.isPending || status === 'SHIPPED' || status === 'DELIVERED'}
                                                    className="flex-1 h-8 text-xs border-amber-300 hover:bg-amber-50 hover:text-amber-700"
                                                >
                                                    {updateStatusMutation.isPending ? (
                                                        <RefreshCw className="w-3 h-3 mr-1 animate-spin" />
                                                    ) : (
                                                        <Truck className="w-3 h-3 mr-1" />
                                                    )}
                                                    Ship
                                                </Button>
                                                <Button
                                                    size="sm"
                                                    variant="outline"
                                                    onClick={(e) => { e.stopPropagation(); setOrderToRefund(order); }}
                                                    disabled={deleteOrderMutation.isPending}
                                                    className="flex-1 h-8 text-xs border-red-300 hover:bg-red-50 hover:text-red-700"
                                                    data-testid="refund-btn"
                                                >
                                                    {deleteOrderMutation.isPending ? (
                                                        <RefreshCw className="w-3 h-3 mr-1 animate-spin" />
                                                    ) : (
                                                        <Trash2 className="w-3 h-3 mr-1" />
                                                    )}
                                                    Refund
                                                </Button>
                                            </div>
                                        )}
                                    </div>
                                );
                            })}
                            {(!recentOrders || recentOrders.length === 0) && !isLoading && (
                                <div className="text-center py-10 text-slate-400 text-sm italic">
                                    No active orders.
                                </div>
                            )}
                            {isLoading && (
                                <div className="text-center py-10 text-slate-400 text-sm">
                                    <RefreshCw className="w-5 h-5 animate-spin mx-auto mb-2" />
                                    Loading orders...
                                </div>
                            )}
                        </div>
                    </div>
                </CardContent>
            </div>

            {/* RIGHT PANEL: CDC Sidecar */}
            <div className="w-full md:w-[320px] bg-[#0F172A] text-slate-300 flex flex-col border-l border-slate-800">
                <div className="p-4 border-b border-slate-800 bg-[#1E293B] flex-shrink-0">
                    <div className="flex items-center justify-between mb-2">
                        <h3 className="font-bold text-sm text-white flex items-center gap-2">
                            <Database className="w-4 h-4 text-sky-400" />
                            CDC Sidecar
                        </h3>
                        <div className="flex items-center gap-1.5">
                            <span className="relative flex h-2 w-2">
                                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75"></span>
                                <span className="relative inline-flex rounded-full h-2 w-2 bg-green-500"></span>
                            </span>
                            <span className="text-[10px] font-mono text-green-400">SYNCING</span>
                        </div>
                    </div>
                    <div className="grid grid-cols-2 gap-2">
                        <div className="bg-slate-900/50 p-2 rounded border border-slate-700">
                            <div className="text-[10px] text-slate-500 uppercase">Repl. Lag</div>
                            <div className="text-xs font-mono font-bold text-green-400">&lt; 15ms</div>
                        </div>
                        <div className="bg-slate-900/50 p-2 rounded border border-slate-700">
                            <div className="text-[10px] text-slate-500 uppercase">WAL LSN</div>
                            <div className="text-xs font-mono font-bold text-sky-400">0/16A4E2</div>
                        </div>
                    </div>
                </div>

                <div className="flex-1 overflow-hidden relative bg-[#0F172A]">
                    <div className="absolute top-0 left-0 right-0 h-4 bg-gradient-to-b from-[#0F172A] to-transparent z-10 pointer-events-none"></div>
                    <ScrollArea className="h-full p-4" ref={scrollRef}>
                        <div className="space-y-3 font-mono text-xs">
                            {cdcLog.length === 0 && (
                                <div className="text-slate-600 text-center py-10 italic">
                                    Waiting for transactions...
                                </div>
                            )}
                            {cdcLog.map((log) => (
                                <div key={log.id} className="animate-in slide-in-from-right-4 duration-300 fade-in-0">
                                    <div className="flex items-center gap-2 mb-1">
                                        <span className="text-slate-500 text-[10px]">{log.time}</span>
                                        <Badge variant="outline" className={`text-[10px] px-1 py-0 h-4 border-0 ${log.op === 'INSERT' ? 'bg-green-900/30 text-green-400' :
                                                log.op === 'UPDATE' ? 'bg-amber-900/30 text-amber-400' :
                                                    'bg-red-900/30 text-red-400'
                                            }`}>
                                            {log.op}
                                        </Badge>
                                    </div>
                                    <div className="pl-2 border-l-2 border-slate-800 ml-1">
                                        <div className="text-sky-300 mb-0.5">TABLE: {log.table}</div>
                                        <div className="text-slate-400 break-all opacity-80">{log.details}</div>
                                        <div className="text-[10px] text-slate-600 mt-1">LSN: {log.lsn}</div>
                                    </div>
                                </div>
                            ))}
                        </div>
                    </ScrollArea>
                    <div className="absolute bottom-0 left-0 right-0 h-12 bg-gradient-to-t from-[#0F172A] to-transparent z-10 pointer-events-none"></div>
                </div>
            </div>

            {/* Refund Confirmation Dialog */}
            <AlertDialog open={!!orderToRefund} onOpenChange={(open) => !open && setOrderToRefund(null)}>
                <AlertDialogContent data-testid="refund-confirm-dialog">
                    <AlertDialogHeader>
                        <AlertDialogTitle>Confirm Refund</AlertDialogTitle>
                        <AlertDialogDescription>
                            Are you sure you want to refund order <strong>#{orderToRefund?.order_id}</strong> for{' '}
                            <strong>{orderToRefund?.customer_full_name}</strong>?
                            <br /><br />
                            This will permanently delete the order and trigger a CDC DELETE event.
                        </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                        <AlertDialogCancel data-testid="refund-cancel-btn">Cancel</AlertDialogCancel>
                        <AlertDialogAction
                            data-testid="refund-confirm-btn"
                            onClick={() => {
                                if (orderToRefund) {
                                    handleRefundOrder(orderToRefund);
                                    setOrderToRefund(null);
                                }
                            }}
                            className="bg-red-600 hover:bg-red-700"
                        >
                            Confirm Refund
                        </AlertDialogAction>
                    </AlertDialogFooter>
                </AlertDialogContent>
            </AlertDialog>
        </Card>
    );
};
