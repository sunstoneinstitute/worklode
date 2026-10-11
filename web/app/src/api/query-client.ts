import { QueryClient } from "@tanstack/react-query"

/**
 * One per app (and a fresh one per test). Tests don't retry, so a failing
 * request fails the test immediately instead of after retry delays.
 */
export function createQueryClient({ test = false }: { test?: boolean } = {}) {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: test ? false : 1, staleTime: 30_000 },
      mutations: { retry: false },
    },
  })
}
