import { useQuery, useMutation, useQueryClient, UseQueryResult, UseMutationResult } from '@tanstack/react-query';
import { get, post } from '@/lib/api-client';
import type { OrderResponse, CreateOrderRequest, CreateOrderResponse } from '@/types/api';

/**
 * Fetch a single order by ID
 *
 * @param orderId - Order ID to fetch
 * @param options - Additional query options
 */
export function useOrder(
  orderId: number | null,
  options?: {
    enabled?: boolean;
  }
): UseQueryResult<OrderResponse, Error> {
  return useQuery({
    queryKey: ['order', orderId],
    queryFn: () =>
      get<OrderResponse>('/api/orders', {
        params: { order_id: orderId },
      }),
    enabled: (options?.enabled ?? true) && orderId !== null,
    staleTime: 5000,
  });
}

/**
 * Create a new order (mutation)
 *
 * Automatically invalidates revenue metrics and top products queries after success
 */
export function useCreateOrder(): UseMutationResult<CreateOrderResponse, Error, CreateOrderRequest> {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (orderData: CreateOrderRequest) => post<CreateOrderResponse>('/api/orders', orderData),
    onSuccess: (data, variables) => {
      // Invalidate and refetch related queries to show updated data
      queryClient.invalidateQueries({ queryKey: ['revenue-metrics', variables.partner_id] });
      queryClient.invalidateQueries({ queryKey: ['top-products', variables.partner_id] });
      queryClient.invalidateQueries({ queryKey: ['funnel', variables.partner_id] });

      // Optimistically add the new order to cache
      queryClient.setQueryData(['order', data.order_id], {
        order_id: data.order_id,
        partner_id: variables.partner_id,
        user_id: variables.user_id,
        status: 'pending',
        created_at: new Date().toISOString(),
        items: variables.items,
      });
    },
  });
}

/**
 * Helper type for transaction list (if backend adds this endpoint)
 * For now, we'll fetch individual orders or use generator status
 */
export interface TransactionListParams {
  partner_id: number;
  limit?: number;
  offset?: number;
}

/**
 * Fetch list of recent orders (if backend supports it)
 * NOTE: This endpoint may not exist yet in the backend
 */
export function useOrders(
  params: TransactionListParams,
  options?: {
    enabled?: boolean;
  }
): UseQueryResult<OrderResponse[], Error> {
  return useQuery({
    queryKey: ['orders', params],
    queryFn: () =>
      get<OrderResponse[]>('/api/orders/list', {
        params,
      }),
    enabled: options?.enabled ?? false, // Disabled by default until backend implements
    staleTime: 5000,
  });
}

/**
 * Recent order from backend
 */
interface RecentOrder {
  order_id: number;
  user_id: number;
  total_amount: number;
  status: string;
  order_date: string;
}

interface RecentOrdersResponse {
  partner_id: number;
  orders: RecentOrder[];
}

/**
 * Fetch recent orders with polling for real-time updates
 *
 * @param partnerId - Partner ID to query
 * @param limit - Number of orders to fetch (default: 20)
 * @param options - Additional query options
 */
export function useRecentOrders(
  partnerId: number,
  limit: number = 20,
  options?: {
    refetchInterval?: number;
    enabled?: boolean;
  }
): UseQueryResult<RecentOrder[], Error> {
  return useQuery({
    queryKey: ['recent-orders', partnerId, limit],
    queryFn: async () => {
      const response = await get<RecentOrdersResponse>(
        '/api/orders/recent',
        { params: { partner_id: partnerId, limit } }
      );
      return response.orders || [];
    },
    refetchInterval: options?.refetchInterval ?? 2000, // Poll every 2 seconds
    enabled: options?.enabled ?? true,
    staleTime: 1000,
  });
}
