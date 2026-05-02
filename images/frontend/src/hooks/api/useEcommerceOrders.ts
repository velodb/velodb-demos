import { useQuery, useMutation, useQueryClient, UseQueryResult, UseMutationResult } from '@tanstack/react-query';
import { get, post, put, del } from '@/lib/api-client';
import type { EcommerceOrder, EcommerceOrdersResponse } from '@/types/api';

/**
 * Fetch recent ecommerce orders with real product names
 *
 * @param limit - Number of orders to fetch (default: 10)
 * @param options - Additional query options
 */
export function useEcommerceOrders(
  limit: number = 10,
  options?: {
    refetchInterval?: number;
    enabled?: boolean;
  }
): UseQueryResult<EcommerceOrder[], Error> {
  return useQuery({
    queryKey: ['ecommerce-orders', limit],
    queryFn: async () => {
      const response = await get<EcommerceOrdersResponse>(
        '/api/ecommerce/orders/recent',
        { params: { limit } }
      );
      return response.orders || [];
    },
    refetchInterval: options?.refetchInterval ?? 3000, // Poll every 3 seconds
    enabled: options?.enabled ?? true,
    staleTime: 2000,
  });
}

/**
 * Update ecommerce order status
 */
interface UpdateStatusRequest {
  orderId: number;
  status: 'pending' | 'processing' | 'shipped' | 'delivered' | 'cancelled';
}

interface UpdateStatusResponse {
  order_id: number;
  old_status: string;
  new_status: string;
  success: boolean;
}

export function useUpdateEcommerceOrderStatus(): UseMutationResult<UpdateStatusResponse, Error, UpdateStatusRequest> {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async ({ orderId, status }: UpdateStatusRequest) => {
      return put<UpdateStatusResponse>(`/api/ecommerce/orders/${orderId}/status`, { status });
    },
    onSuccess: () => {
      // Invalidate ecommerce orders to refresh the list
      queryClient.invalidateQueries({ queryKey: ['ecommerce-orders'] });
    },
  });
}

/**
 * Delete ecommerce order (hard delete / refund)
 */
interface DeleteOrderResponse {
  order_id: number;
  deleted: boolean;
  success: boolean;
}

export function useDeleteEcommerceOrder(): UseMutationResult<DeleteOrderResponse, Error, number> {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (orderId: number) => {
      return del<DeleteOrderResponse>(`/api/ecommerce/orders/${orderId}`);
    },
    onSuccess: () => {
      // Invalidate ecommerce orders to refresh the list
      queryClient.invalidateQueries({ queryKey: ['ecommerce-orders'] });
    },
  });
}

/**
 * Create a new ecommerce order
 */
export interface CreateOrderResponse {
  order_id: number;
  order_date: string;
  customer_name: string;
  total_price: number;
  currency: string;
  product_name: string;
  city: string;
  category: string[];
  order_status: string;
  products: Array<{
    product_name: string;
    price: number;
    quantity: number;
  }>;
  geoip: {
    city_name: string;
    continent_name: string;
    country_iso_code: string;
  };
}

export function useCreateEcommerceOrder(): UseMutationResult<CreateOrderResponse, Error, void> {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async () => {
      return post<CreateOrderResponse>('/api/ecommerce/orders');
    },
    onSuccess: () => {
      // Invalidate ecommerce orders to refresh the list
      queryClient.invalidateQueries({ queryKey: ['ecommerce-orders'] });
    },
  });
}
