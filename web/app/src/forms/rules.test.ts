import { describe, expect, it } from "vitest"
import { cellErrorsFor, isShown, requiredFields, startingValues, submittedValues, validate, valuesDiffer, visibleFields } from "./rules"
import type { FormConfig, FormLayout, FormSchema } from "./types"

const schema: FormSchema = {
  type: "object",
  required: ["title"],
  properties: {
    title: { type: "string", title: "Title", minLength: 1, errorMessage: "Give it a title." },
    type: { type: "string", title: "Type", oneOf: [{ const: "a", title: "A" }, { const: "b", title: "B" }] },
    story: { type: "string", title: "Story" },
    due: { type: "string", title: "Due", format: "date" },
    people: { type: "array", title: "People" },
    crew: {
      type: "array",
      title: "Crew",
      items: {
        type: "object",
        required: ["user"],
        properties: {
          user: { type: "string", errorMessage: "Pick a person." },
          date: { type: "string", format: "date" },
          lead: { type: "boolean", maxContains: 1, errorMessage: "Pick one Lead at most." },
          approved: { type: "boolean", readOnly: true },
        },
      },
    },
  },
  if: { properties: { type: { const: "b" } } },
  then: { required: ["story"] },
}

const layout: FormLayout = {
  type: "single",
  columns: {
    main: [
      "title",
      "type",
      { field: "story", showWhen: { field: "type", equals: "b" } },
      { field: "due", showWhen: { field: "type", in: ["a", "b"] } },
      { field: "people", sendWhenChanged: true },
      { field: "crew", widget: "rows" },
      { block: "note", markdown: "Hello" },
    ],
  },
}

describe("visibility", () => {
  it("applies equals and in", () => {
    expect(isShown({ field: "type", equals: "b" }, { type: "a" })).toBe(false)
    expect(isShown({ field: "type", in: ["a", "b"] }, { type: "a" })).toBe(true)
    expect(isShown(undefined, {})).toBe(true)
  })

  it("lists shown fields only, in layout order, with no blocks", () => {
    expect(visibleFields(layout, { type: "a" })).toEqual(["title", "type", "due", "people", "crew"])
  })
})

describe("requiredFields", () => {
  it("adds a matching if's then, top level and inside allOf", () => {
    expect(requiredFields(schema, { type: "b" })).toEqual(new Set(["title", "story"]))
    expect(requiredFields(schema, { type: "a" })).toEqual(new Set(["title"]))
    const nested: FormSchema = {
      type: "object",
      properties: {},
      allOf: [{ if: { properties: { type: { const: "b" } } }, then: { required: ["x"] }, else: { required: ["y"] } }],
    }
    expect(requiredFields(nested, { type: "b" })).toEqual(new Set(["x"]))
    expect(requiredFields(nested, { type: "a" })).toEqual(new Set(["y"]))
  })
})

describe("validate", () => {
  it("reports a required field with its errorMessage or its title", () => {
    expect(validate(schema, { title: "" })).toEqual({ title: "Give it a title." })
    expect(validate(schema, { title: "x", type: "b", story: "" })).toEqual({ story: "Story is required." })
  })

  it("does not check the format of an empty optional field", () => {
    expect(validate(schema, { title: "x", due: "" })).toEqual({})
  })

  it("checks a date and a oneOf", () => {
    expect(validate(schema, { title: "x", due: "soon", type: "c" })).toEqual({
      due: "Due must be a date.",
      type: "Type must be one of the choices.",
    })
    expect(validate(schema, { title: "x", due: "2026-02-30" })).toHaveProperty("due")
    expect(validate(schema, { title: "x", due: "2026-02-28" })).toEqual({})
  })

  it("checks rows cell by cell and maxContains on the list", () => {
    const errors = validate(schema, {
      title: "x",
      crew: [
        { user: "", date: "soon", lead: true },
        { user: "u", lead: true },
      ],
    })
    expect(errors).toEqual({
      "crew/0/user": "Pick a person.",
      "crew/0/date": "date must be a date.",
      crew: "Pick one Lead at most.",
    })
    expect(cellErrorsFor(errors, "crew")).toEqual({ "0/user": "Pick a person.", "0/date": "date must be a date." })
  })
})

describe("submittedValues", () => {
  const initial = { title: "", type: "a", story: "kept", due: "", people: ["u"], crew: [] }

  it("sends shown fields trimmed, keeps empties so an edit can clear them", () => {
    const sent = submittedValues(layout, schema, { ...initial, title: "  x  ", due: "" }, initial)
    expect(sent).toEqual({ title: "x", type: "a", due: "", crew: [] })
  })

  it("leaves out a sendWhenChanged field until it changes", () => {
    expect(submittedValues(layout, schema, { ...initial, people: ["v"] }, initial)).toHaveProperty("people", ["v"])
  })

  it("never sends a locked field or a readOnly row property, and trims row text", () => {
    const sent = submittedValues(layout, schema, { ...initial, crew: [{ user: " u ", approved: true }] }, initial, { type: "fixed" })
    expect(sent).not.toHaveProperty("type")
    expect(sent.crew).toEqual([{ user: "u" }])
  })
})

describe("startingValues", () => {
  it("takes the record, then defaults, then the type's empty value, for schema keys only", () => {
    const config = { id: "x", schema, layout, defaults: { type: "a" } } satisfies FormConfig
    expect(startingValues(config, { title: "t", id: "r", story: null })).toEqual({
      title: "t", type: "a", story: "", due: "", people: [], crew: [],
    })
  })
})

describe("valuesDiffer", () => {
  it("treats a missing value and an empty one alike", () => {
    expect(valuesDiffer({ a: "" }, {})).toBe(false)
    expect(valuesDiffer({ a: [1] }, { a: [2] })).toBe(true)
  })
})
