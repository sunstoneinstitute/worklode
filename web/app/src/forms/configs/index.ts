import type { components } from "@/api/schema"
import type { FormConfig } from "../types"
import ideaJson from "./idea.json"
import taskJson from "./task.json"

type RequiredKeys<Body> = { [K in keyof Body]-?: undefined extends Body[K] ? never : K }[keyof Body]
type Listed<Config> = Config extends { schema: { properties: infer P } } ? keyof P : never
type Defaulted<Config> = Config extends { defaults: infer D } ? keyof D : never

/** `never`, which no config satisfies, unless the config fits the route's body (WL-REQ-1887) */
type Fits<Body, Config> = [Listed<Config>] extends [keyof Body]
  ? [RequiredKeys<Body>] extends [Listed<Config> | Defaulted<Config>]
    ? Config
    : never
  : never

/**
 * Pairs a config with its submit route's request body, so a config naming a
 * field the Go type lacks, or leaving out one it requires, fails typecheck.
 * It is curried because TypeScript cannot infer `Config` beside a given `Body`.
 */
export const form =
  <Body>() =>
  <Config>(config: Fits<Body, Config>) =>
    config as unknown as FormConfig

export const ideaForm = form<components["schemas"]["CreateIdeaInput"] & components["schemas"]["PatchIdeaInput"]>()(ideaJson)
export const taskForm = form<components["schemas"]["EditTaskInput"]>()(taskJson)

export const CONFIGS = [ideaForm, taskForm]
