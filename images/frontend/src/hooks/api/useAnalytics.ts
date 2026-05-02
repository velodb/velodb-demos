import { useQuery, UseQueryResult } from '@tanstack/react-query';
import { get } from '@/lib/api-client';
import type { RevenueMetricsResponse, FunnelResponse, TopProductsResponse } from '@/types/api';

/**
 * Fetch revenue metrics for a partner over specified hours
 *
 * @param partnerId - Partner ID to query
 * @param hours - Number of hours to query (default: 24)
 * @param options - Additional query options
 */
export function useRevenueMetrics(
  partnerId: number,
  hours: number = 24,
  options?: {
    refetchInterval?: number;
    enabled?: boolean;
  }
): UseQueryResult<RevenueMetricsResponse, Error> {
  return useQuery({
    queryKey: ['revenue-metrics', partnerId, hours],
    queryFn: () =>
      get<RevenueMetricsResponse>('/api/metrics/revenue', {
        params: { partner_id: partnerId, hours },
      }),
    refetchInterval: options?.refetchInterval ?? 10000, // Auto-refresh every 10 seconds
    enabled: options?.enabled ?? true,
    staleTime: 5000, // Consider data stale after 5 seconds
  });
}

/**
 * Fetch conversion funnel data for a partner over specified days
 *
 * @param partnerId - Partner ID to query
 * @param days - Number of days to query (default: 7)
 * @param options - Additional query options
 */
export function useFunnel(
  partnerId: number,
  days: number = 7,
  options?: {
    refetchInterval?: number;
    enabled?: boolean;
  }
): UseQueryResult<FunnelResponse, Error> {
  return useQuery({
    queryKey: ['funnel', partnerId, days],
    queryFn: () =>
      get<FunnelResponse>('/api/funnel', {
        params: { partner_id: partnerId, days },
      }),
    refetchInterval: options?.refetchInterval ?? 15000, // Auto-refresh every 15 seconds
    enabled: options?.enabled ?? true,
    staleTime: 10000,
  });
}

/**
 * Fetch top products by revenue for a partner
 *
 * @param partnerId - Partner ID to query
 * @param limit - Number of products to return (default: 20)
 * @param options - Additional query options
 */
export function useTopProducts(
  partnerId: number,
  limit: number = 20,
  options?: {
    refetchInterval?: number;
    enabled?: boolean;
  }
): UseQueryResult<TopProductsResponse, Error> {
  return useQuery({
    queryKey: ['top-products', partnerId, limit],
    queryFn: () =>
      get<TopProductsResponse>('/api/products/top', {
        params: { partner_id: partnerId, limit },
      }),
    refetchInterval: options?.refetchInterval ?? 15000, // Auto-refresh every 15 seconds
    enabled: options?.enabled ?? true,
    staleTime: 10000,
  });
}
