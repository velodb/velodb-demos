import { useQuery, UseQueryResult } from '@tanstack/react-query';
import { get } from '@/lib/api-client';
import type { HealthResponse, MetricsResponse } from '@/types/api';

/**
 * Fetch backend health status
 *
 * @param options - Additional query options
 */
export function useHealth(options?: {
  refetchInterval?: number;
  enabled?: boolean;
}): UseQueryResult<HealthResponse, Error> {
  return useQuery({
    queryKey: ['health'],
    queryFn: () => get<HealthResponse>('/health'),
    refetchInterval: options?.refetchInterval ?? 30000, // Check every 30 seconds
    enabled: options?.enabled ?? true,
    staleTime: 10000,
    retry: 3, // Retry health checks
  });
}

/**
 * Fetch backend metrics
 *
 * @param options - Additional query options
 */
export function useMetrics(options?: {
  refetchInterval?: number;
  enabled?: boolean;
}): UseQueryResult<MetricsResponse, Error> {
  return useQuery({
    queryKey: ['metrics'],
    queryFn: () => get<MetricsResponse>('/metrics'),
    refetchInterval: options?.refetchInterval ?? 10000, // Check every 10 seconds
    enabled: options?.enabled ?? true,
    staleTime: 5000,
  });
}
