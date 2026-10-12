import type { paths } from "@/api/schema"

/**
 * The 200 JSON body of a generated route. Typing a mock with it makes a mock
 * shaped unlike the Go type fail `pnpm typecheck` (WL-REQ-1309).
 */
export type Ok<P extends keyof paths, M extends keyof paths[P]> =
  paths[P][M] extends { responses: { 200: { content: { "application/json": infer B } } } } ? B : never
