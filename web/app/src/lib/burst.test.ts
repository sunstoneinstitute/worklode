import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { burstCoffee, burstStars, burstThumbsUp, celebrate } from "./burst"

function mockReducedMotion(reduce: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({ matches: reduce && query.includes("reduce") }))
}

describe.each([
  ["burstStars", burstStars, "wlicon-star"],
  ["burstThumbsUp", burstThumbsUp, "wlicon-star"],
  ["burstCoffee", burstCoffee, "wlicon-star"],
] as const)("%s", (_name, burst, glyphClass) => {
  let finish: () => void

  beforeEach(() => {
    // jsdom has no Web Animations API: stand in an animate() whose
    // `finished` promise the test resolves by hand.
    const finished = new Promise<void>((resolve) => (finish = resolve))
    Element.prototype.animate = vi.fn(() => ({ finished }) as unknown as Animation)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    document.body.innerHTML = ""
  })

  it("adds a hidden overlay of glyphs at the given point", () => {
    mockReducedMotion(false)

    burst(120, 80)

    const overlay = document.body.querySelector("[aria-hidden=true]") as HTMLElement
    expect(overlay.style.left).toBe("120px")
    expect(overlay.style.top).toBe("80px")
    expect(overlay.querySelectorAll(`i.${glyphClass}`).length).toBeGreaterThan(0)
  })

  it("removes the overlay once everything finishes", async () => {
    mockReducedMotion(false)

    burst(0, 0)
    finish()
    await vi.waitFor(() => expect(document.body.children).toHaveLength(0))
  })

  it("does nothing when the user prefers reduced motion", () => {
    mockReducedMotion(true)

    burst(0, 0)

    expect(document.body.children).toHaveLength(0)
  })
})

describe("burstThumbsUp", () => {
  beforeEach(() => {
    Element.prototype.animate = vi.fn(() => ({ finished: new Promise(() => {}) }) as unknown as Animation)
    mockReducedMotion(false)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    document.body.innerHTML = ""
  })

  it("bursts stars, with exactly one thumbs-up: big, viewport-sized, headed for the middle", () => {
    vi.spyOn(document.documentElement, "clientWidth", "get").mockReturnValue(1000)
    vi.spyOn(document.documentElement, "clientHeight", "get").mockReturnValue(800)

    burstThumbsUp(100, 100)

    expect(document.body.querySelectorAll("i.wlicon-star").length).toBeGreaterThan(0)
    const thumbs = [...document.body.querySelectorAll<HTMLElement>("i.wlicon-thumb-up")]
    expect(thumbs).toHaveLength(1)
    const [hero] = thumbs
    expect(hero.style.fontSize).toBe("20vw")

    const heroCall = vi.mocked(Element.prototype.animate).mock.calls.find(
      (_call, index) => vi.mocked(Element.prototype.animate).mock.contexts[index] === hero,
    )
    const keyframes = heroCall?.[0] as Keyframe[]
    // Ends at viewport center: (500 - 100, 400 - 100)
    expect(String(keyframes.at(-1)?.transform)).toContain("translate(400px, 300px)")
  })
})

describe("burstCoffee", () => {
  beforeEach(() => {
    Element.prototype.animate = vi.fn(() => ({ finished: new Promise(() => {}) }) as unknown as Animation)
    mockReducedMotion(false)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ""
  })

  it("lays a row of cups across the screen, each starting a beat after the one to its left", () => {
    vi.spyOn(document.documentElement, "clientWidth", "get").mockReturnValue(1000)

    burstCoffee(100, 100)

    const cups = [...document.body.querySelectorAll<HTMLElement>("i.wlicon-coffee")]
    // (1000 - 128 + 20) / 84 → 10 cups
    expect(cups).toHaveLength(10)
    const animate = vi.mocked(Element.prototype.animate)
    const delays = cups.map((cup) => {
      const index = animate.mock.contexts.indexOf(cup)
      return (animate.mock.calls[index][1] as KeyframeAnimationOptions).delay
    })
    expect(delays).toEqual([...delays].sort((a = 0, b = 0) => a - b))
    expect(new Set(delays).size).toBe(cups.length)
  })
})

describe("celebrate", () => {
  beforeEach(() => {
    Element.prototype.animate = vi.fn(() => ({ finished: new Promise(() => {}) }) as unknown as Animation)
    mockReducedMotion(false)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ""
  })

  // First roll: special (below 1/6) or stars. Second roll: which special.
  it.each([
    [[0.5, 0.2], "stars only", 0, 0],
    [[0.17, 0.2], "stars only (just over 1/6)", 0, 0],
    [[0.16, 0.2], "thumbs up", 1, 0],
    [[0.16, 0.8], "coffee", 0, 1],
  ])("with rolls %j plays %s", (rolls, _name, thumbs, coffeeRows) => {
    const random = vi.spyOn(Math, "random")
    for (const roll of rolls) random.mockReturnValueOnce(roll)
    random.mockReturnValue(0.5) // the burst's own jitter

    celebrate(0, 0)

    expect(document.body.querySelectorAll("i.wlicon-star").length).toBeGreaterThan(0)
    expect(document.body.querySelectorAll("i.wlicon-thumb-up")).toHaveLength(thumbs)
    expect(document.body.querySelectorAll("i.wlicon-coffee").length > 0 ? 1 : 0).toBe(coffeeRows)
  })
})
