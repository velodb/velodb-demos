import { useQuery, UseQueryResult } from '@tanstack/react-query';
import { get } from '@/lib/api-client';
import type { RecentClickstreamEvent } from '@/types/api';

/**
 * Fetch recent clickstream events from VeloDB
 *
 * @param partnerId - Partner ID to query
 * @param limit - Number of events to fetch (default: 20)
 * @param options - Additional query options
 */
export function useRecentClickstream(
  partnerId: number,
  limit: number = 20,
  options?: {
    refetchInterval?: number;
    enabled?: boolean;
  }
): UseQueryResult<RecentClickstreamEvent[], Error> {
  return useQuery({
    queryKey: ['recent-clickstream', partnerId, limit],
    queryFn: async () => {
      const response = await get<{ partner_id: number; events: RecentClickstreamEvent[] }>(
        '/api/clickstream/recent',
        { params: { partner_id: partnerId, limit } }
      );
      return response.events || [];
    },
    refetchInterval: options?.refetchInterval ?? 2000, // Poll every 2 seconds for real-time updates
    enabled: options?.enabled ?? true,
    staleTime: 1000, // Consider stale after 1 second
  });
}
