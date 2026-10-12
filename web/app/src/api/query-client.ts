import { QueryClient } from "@tanstack/react-query"
import { HttpError } from "./queries"

/**
 * Retry a failed read once, unless the server answered 4xx: asking again
 * for an unknown id gets the same answer.
 */
export function retryRead(failureCount: number, error: Error) {
  return failureCount < 1 && !(error instanceof HttpError && error.status < 500)
}

/**
 * One per app (and a fresh one per test). Tests don't retry, so a failing
 * request fails the test immediately instead of after retry delays.
 */
export function createQueryClient({ test = false }: { test?: boolean } = {}) {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: test ? false : retryRead, staleTime: 30_000 },
      mutations: { retry: false },
    },
  })
}
