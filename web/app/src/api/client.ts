/** A non-2xx response. `errors` holds per-field messages from a 422. */
export class ApiError extends Error {
  status: number
  errors?: Record<string, string>

  constructor(status: number, message: string, errors?: Record<string, string>) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.errors = errors
  }
}

/**
 * Calls the API and returns its parsed JSON (undefined for 204). Paths are
 * relative ("/api/tasks") and resolved against the page's origin, which
 * also makes them work under jsdom in tests, where fetch needs a full URL.
 */
export async function apiFetch<T>(
  path: string,
  { method = "GET", body }: { method?: string; body?: unknown } = {},
): Promise<T> {
  const response = await fetch(new URL(path, window.location.origin), {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })

  if (!response.ok) {
    // `?? {}` also covers a body whose JSON parses fine but *is* null
    // (e.g. `null`), which .catch() alone doesn't catch.
    const payload = ((await response.json().catch(() => ({}))) ?? {}) as {
      message?: string
      errors?: Record<string, string>
    }
    throw new ApiError(
      response.status,
      payload.message ?? `${response.status} ${response.statusText}`.trim(),
      payload.errors,
    )
  }

  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}
