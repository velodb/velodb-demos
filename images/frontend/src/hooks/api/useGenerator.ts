import { useQuery, useMutation, useQueryClient, UseQueryResult, UseMutationResult } from '@tanstack/react-query';
import { get, post, del } from '@/lib/api-client';
import type {
  GeneratorStatusResponse,
  GeneratorMessageResponse,
  TrafficSpikeRequest,
  GeneratorRateRequest,
  GeneratorConfigRequest,
} from '@/types/api';

/**
 * Fetch current generator status
 *
 * @param options - Additional query options
 */
export function useGeneratorStatus(options?: {
  refetchInterval?: number;
  enabled?: boolean;
}): UseQueryResult<GeneratorStatusResponse, Error> {
  return useQuery({
    queryKey: ['generator-status'],
    queryFn: () => get<GeneratorStatusResponse>('/api/generator/status'),
    refetchInterval: options?.refetchInterval ?? 3000, // Poll every 3 seconds for live updates
    enabled: options?.enabled ?? true,
    staleTime: 2000,
  });
}

/**
 * Start data generators (mutation)
 */
export function useStartGenerator(): UseMutationResult<GeneratorMessageResponse, Error, void> {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: () => post<GeneratorMessageResponse>('/api/generator/start'),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['generator-status'] });
    },
  });
}

/**
 * Stop data generators (mutation)
 */
export function useStopGenerator(): UseMutationResult<GeneratorMessageResponse, Error, void> {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: () => post<GeneratorMessageResponse>('/api/generator/stop'),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['generator-status'] });
    },
  });
}

/**
 * Trigger traffic spike (mutation)
 *
 * @param request - Spike configuration (service, multiplier, duration)
 */
export function useTrafficSpike(): UseMutationResult<GeneratorMessageResponse, Error, TrafficSpikeRequest> {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (request: TrafficSpikeRequest) => post<GeneratorMessageResponse>('/api/generator/spike', request),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['generator-status'] });
    },
  });
}

/**
 * Cancel traffic spike (mutation)
 *
 * @param service - Which service to cancel spike for (optional, defaults to 'all')
 */
export function useCancelSpike(): UseMutationResult<GeneratorMessageResponse, Error, string | undefined> {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (service?: string) =>
      del<GeneratorMessageResponse>('/api/generator/spike', {
        params: service ? { service } : undefined,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['generator-status'] });
    },
  });
}

/**
 * Update generator rate (mutation)
 *
 * @param request - Rate configuration (service, rate)
 */
export function useUpdateGeneratorRate(): UseMutationResult<GeneratorMessageResponse, Error, GeneratorRateRequest> {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (request: GeneratorRateRequest) => post<GeneratorMessageResponse>('/api/generator/rate', request),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['generator-status'] });
    },
  });
}

/**
 * Update generator configuration (mutation)
 *
 * @param request - Configuration update (baseline rates)
 */
export function useUpdateGeneratorConfig(): UseMutationResult<
  GeneratorMessageResponse,
  Error,
  GeneratorConfigRequest
> {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (request: GeneratorConfigRequest) => post<GeneratorMessageResponse>('/api/generator/config', request),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['generator-status'] });
    },
  });
}
