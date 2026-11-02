import { describe, expect, it } from "vitest"
import { IDEA_TYPE_OPTIONS, STORY_TYPE_OPTIONS } from "@/lib/content-types"
import metaSchema from "../form-config.schema.json"
import { allItems, fieldItems } from "../rules"
import type { FieldSchema, FormConfig } from "../types"
import { CONFIGS, form, ideaForm } from "."

// A check of the keywords form-config.schema.json uses, not a JSON Schema
// validator: the SPA carries none (WL-REQ-1888).
type Meta = Record<string, never> & {
  $ref?: string
  type?: string
  const?: unknown
  enum?: unknown[]
  pattern?: string
  minimum?: number
  maximum?: number
  required?: string[]
  properties?: Record<string, Meta>
  additionalProperties?: boolean
  items?: Meta
  oneOf?: Meta[]
  anyOf?: Meta[]
}

function typeOf(value: unknown) {
  if (Array.isArray(value)) return "array"
  return Number.isInteger(value) ? "integer" : typeof value
}

function check(meta: Meta, value: unknown, path: string): string[] {
  if (meta.$ref) return check((metaSchema.$defs as unknown as Record<string, Meta>)[meta.$ref.replace("#/$defs/", "")]!, value, path)
  const errors: string[] = []
  if (meta.type && typeOf(value) !== meta.type) return [`${path}: not ${meta.type}`]
  if ("const" in meta && value !== meta.const) errors.push(`${path}: not ${String(meta.const)}`)
  if (meta.enum && !meta.enum.includes(value)) errors.push(`${path}: ${String(value)} is not in the enum`)
  if (meta.pattern && !new RegExp(meta.pattern).test(String(value))) errors.push(`${path}: does not match ${meta.pattern}`)
  if (meta.minimum !== undefined && (value as number) < meta.minimum) errors.push(`${path}: too small`)
  if (meta.maximum !== undefined && (value as number) > meta.maximum) errors.push(`${path}: too big`)
  if (typeof value === "object" && value !== null && !Array.isArray(value)) {
    const object = value as Record<string, unknown>
    for (const key of meta.required ?? []) if (!(key in object)) errors.push(`${path}: lacks ${key}`)
    for (const [key, cell] of Object.entries(object)) {
      const property = meta.properties?.[key]
      if (property) errors.push(...check(property, cell, `${path}.${key}`))
      else if (meta.additionalProperties === false) errors.push(`${path}: unknown key ${key}`)
    }
  }
  if (Array.isArray(value) && meta.items) value.forEach((cell, index) => errors.push(...check(meta.items!, cell, `${path}[${index}]`)))
  if (meta.oneOf && meta.oneOf.filter((option) => check(option, value, path).length === 0).length !== 1) errors.push(`${path}: fits not exactly one of oneOf`)
  if (meta.anyOf && !meta.anyOf.some((option) => check(option, value, path).length === 0)) errors.push(`${path}: fits none of anyOf`)
  return errors
}

const FIELD_KEYWORDS = new Set(["type", "title", "description", "required", "minLength", "oneOf", "format", "readOnly", "items", "maxContains", "errorMessage", "x-options"])
const TOP_KEYWORDS = new Set(["type", "properties", "required", "if", "then", "else", "allOf"])

function fieldKeywordErrors(field: FieldSchema, path: string): string[] {
  const errors = Object.keys(field).filter((key) => !FIELD_KEYWORDS.has(key)).map((key) => `${path}: keyword ${key}`)
  if (field.format && field.format !== "date") errors.push(`${path}: format ${field.format}`)
  for (const option of field.oneOf ?? []) {
    if (Object.keys(option).some((key) => key !== "const" && key !== "title")) errors.push(`${path}: oneOf entry`)
  }
  if (field.items) {
    const { type, title, required, properties, ...rest } = field.items
    void [type, title, required]
    errors.push(...Object.keys(rest).map((key) => `${path}.items: keyword ${key}`))
    if (properties) for (const [key, cell] of Object.entries(properties)) errors.push(...fieldKeywordErrors(cell, `${path}.${key}`))
  }
  return errors
}

function schemaKeywordErrors({ schema }: FormConfig) {
  const errors = Object.keys(schema).filter((key) => !TOP_KEYWORDS.has(key)).map((key) => `schema: keyword ${key}`)
  for (const [key, field] of Object.entries(schema.properties)) errors.push(...fieldKeywordErrors(field, key))
  for (const rule of [schema, ...(schema.allOf ?? [])]) {
    if (rule.if && Object.keys(rule.if).some((key) => key !== "properties")) errors.push("if: tests more than properties")
    for (const test of Object.values(rule.if?.properties ?? {})) if (!("const" in test)) errors.push("if: tests more than const")
    for (const branch of [rule.then, rule.else]) if (branch && Object.keys(branch).some((key) => key !== "required")) errors.push("branch: adds more than required")
  }
  return errors
}

describe.each(CONFIGS.map((config) => [config.id, config] as const))("config %s", (_, config) => {
  it("fits form-config.schema.json", () => {
    expect(check(metaSchema as unknown as Meta, config, config.id)).toEqual([])
  })

  it("uses only the keywords rules.ts interprets", () => {
    expect(schemaKeywordErrors(config)).toEqual([])
  })

  it("names only schema.properties in its layout and showWhen", () => {
    const known = new Set(Object.keys(config.schema.properties))
    const named = [...fieldItems(config.layout).map((item) => item.field), ...allItems(config.layout).flatMap((item) => (item.showWhen ? [item.showWhen.field] : []))]
    expect(named.filter((field) => !known.has(field))).toEqual([])
  })

  it("uses only {id} or a field as a placeholder", () => {
    const urls = [config.create?.submit.url, config.edit?.submit.url, ...Object.values(config.schema.properties).map((field) => field["x-options"])]
    const names = urls.flatMap((url) => [...(url ?? "").matchAll(/\{(\w+)\}/g)].map((match) => match[1]!))
    expect(names.filter((name) => name !== "id" && !(name in config.schema.properties))).toEqual([])
  })
})

it("types a config against its route's body", () => {
  type Body = { title?: string; kind: string }
  const fits = { schema: { properties: { title: {} } }, defaults: { kind: "bug" } }
  expect(form<Body>()(fits)).toBe(fits)
  // @ts-expect-error `nope` is no key of Body
  form<Body>()({ schema: { properties: { title: {}, kind: {}, nope: {} } } })
  // @ts-expect-error `kind` is required, and neither listed nor defaulted
  form<Body>()({ schema: { properties: { title: {} } } })
})

it("keeps the idea selects in step with the lists the Ideas table shows", () => {
  const options = (field: string) => ideaForm.schema.properties[field]?.oneOf?.map((option) => ({ value: option.const, label: option.title }))
  expect(options("type")).toEqual(IDEA_TYPE_OPTIONS)
  expect(options("story_type")).toEqual(STORY_TYPE_OPTIONS)
})
