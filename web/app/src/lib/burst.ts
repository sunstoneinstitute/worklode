// Easter-egg celebrations: a small firework of icons that burst outward
// from a point, then drift down and fade. Plain DOM + the Web Animations
// API, no React: each call appends its own short-lived overlay to <body>
// and removes it once every icon has finished, so it can fire from anywhere
// (and several can overlap) without any component state.
//
// Two variants, both using icon-font glyphs (Material Symbols, filled):
// - burstStars: blue stars.
// - burstThumbsUp: the same star burst, plus one big blue-gradient thumbs-up
//   that spins as it flies to the middle of the screen, grows, holds for a
//   moment and fades — giving the user a thumbs up.
// - burstCoffee: the same star burst, plus a row of coffee cups along the
//   bottom of the screen that appear left to right, one after another,
//   then fade out left to right the same way.
// celebrate() plays the plain stars 5 times in 6; otherwise it's an even
// pick between the thumbs-up and the coffee.

const BURST_COUNT = 14
const BURST_DURATION_MS = 1400

// Two brand blues, so the burst has a little depth instead of one flat color
const COLORS = ["var(--color-highlight)", "var(--color-primary)"]

// The big center thumb: fly in, hold, fade. Sized to the viewport — any
// CSS length works here.
const HERO_SIZE = "20vw"
const HERO_FLY_MS = 500
const HERO_HOLD_MS = 1500
const HERO_FADE_MS = 400

// The coffee row: cup size and spacing, plus per-cup timing. Each cup
// starts COFFEE_STAGGER_MS after the one before it, so both the appearing
// and the fading run left to right.
const COFFEE_SIZE_PX = 64
const COFFEE_GAP_PX = 20
const COFFEE_STAGGER_MS = 12
const COFFEE_IN_MS = 75
const COFFEE_HOLD_MS = 135
const COFFEE_OUT_MS = 75

function random(min: number, max: number) {
  return min + Math.random() * (max - min)
}

function prefersReducedMotion() {
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches
}

/** A fixed, click-through overlay anchored at viewport point (x, y). */
function createOverlay(x: number, y: number) {
  const overlay = document.createElement("div")
  overlay.setAttribute("aria-hidden", "true")
  Object.assign(overlay.style, {
    position: "fixed",
    left: `${x}px`,
    top: `${y}px`,
    pointerEvents: "none",
    zIndex: "100",
  })
  document.body.append(overlay)
  return overlay
}

/**
 * An icon-font glyph centered on its overlay's anchor point. `size` is px
 * as a number, or any CSS length.
 */
function createGlyph(glyph: string, size: number | string, color: string) {
  const length = typeof size === "number" ? `${size}px` : size
  const icon = document.createElement("i")
  icon.className = `wlicon-${glyph} leading-none`
  Object.assign(icon.style, {
    position: "absolute",
    left: "0",
    top: "0",
    fontSize: length,
    // Half the glyph's 1em box, so its center sits on the anchor
    marginLeft: `calc(${length} / -2)`,
    marginTop: `calc(${length} / -2)`,
    color,
  })
  return icon
}

function burstGlyphs(overlay: HTMLElement, glyph: string) {
  return Array.from({ length: BURST_COUNT }, (_, index) => {
    const icon = createGlyph(glyph, random(12, 22), COLORS[index % COLORS.length])
    overlay.append(icon)

    // Evenly spaced around the circle, jittered so it doesn't look stamped
    const angle = (index / BURST_COUNT) * Math.PI * 2 + random(-0.3, 0.3)
    const distance = random(40, 90)
    const dx = Math.cos(angle) * distance
    const dy = Math.sin(angle) * distance
    // After the burst each icon falls a bit and sways sideways
    const fall = random(40, 80)
    const sway = random(-15, 15)
    const spin = random(-180, 180)

    return icon.animate(
      [
        {
          transform: "translate(0, 0) scale(0.3) rotate(0deg)",
          opacity: 1,
          // Fast out, sharp slowdown — the "explode"
          easing: "cubic-bezier(0.1, 0.8, 0.3, 1)",
        },
        {
          offset: 0.3,
          transform: `translate(${dx}px, ${dy}px) scale(1) rotate(${spin / 2}deg)`,
          opacity: 1,
          // Gentle acceleration downward — the "drift"
          easing: "cubic-bezier(0.4, 0, 0.8, 0.6)",
        },
        {
          transform: `translate(${dx + sway}px, ${dy + fall}px) scale(0.7) rotate(${spin}deg)`,
          opacity: 0,
        },
      ],
      { duration: BURST_DURATION_MS * random(0.85, 1.15), fill: "forwards" },
    )
  })
}

function removeWhenDone(overlay: HTMLElement, animations: Animation[]) {
  Promise.all(animations.map((animation) => animation.finished)).finally(() => overlay.remove())
}

/** Burst stars out from viewport coordinates (x, y). */
export function burstStars(x: number, y: number) {
  if (prefersReducedMotion()) return
  const overlay = createOverlay(x, y)
  removeWhenDone(overlay, burstGlyphs(overlay, "star"))
}

/**
 * Burst stars out from viewport coordinates (x, y), and send one big
 * thumbs-up from there to the middle of the screen.
 */
export function burstThumbsUp(x: number, y: number) {
  if (prefersReducedMotion()) return
  const overlay = createOverlay(x, y)
  const animations = burstGlyphs(overlay, "star")

  const hero = createGlyph("thumb-up", HERO_SIZE, "transparent")
  // Brand-blue gradient (highlight → primary) clipped to the glyph's shape
  Object.assign(hero.style, {
    backgroundImage: "linear-gradient(to bottom, var(--color-highlight), var(--color-primary))",
    backgroundClip: "text",
    webkitBackgroundClip: "text",
  })
  // A big, soft white glow rather than a dark shadow
  hero.style.filter = "drop-shadow(0 8px 32px rgb(255 255 255 / 0.95))"
  overlay.append(hero)
  // Travel from the burst point to the viewport center — clientWidth/
  // clientHeight, not innerWidth/innerHeight, which include the scrollbar
  // and put the landing spot a few px right of true center
  const { clientWidth, clientHeight } = document.documentElement
  const dx = clientWidth / 2 - x
  const dy = clientHeight / 2 - y
  const total = HERO_FLY_MS + HERO_HOLD_MS + HERO_FADE_MS
  const arrive = HERO_FLY_MS / total
  const fadeStart = (HERO_FLY_MS + HERO_HOLD_MS) / total
  animations.push(
    hero.animate(
      [
        {
          // One full spin on the way in (-360° → the 4° landing tilt)
          transform: "translate(0, 0) scale(0.15) rotate(-360deg)",
          opacity: 0.6,
          easing: "cubic-bezier(0.2, 0.9, 0.3, 1)",
        },
        // Lands with a slight overshoot, then settles — the "pop"
        { offset: arrive, transform: `translate(${dx}px, ${dy}px) scale(1.12) rotate(4deg)`, opacity: 1 },
        { offset: arrive + 0.06, transform: `translate(${dx}px, ${dy}px) scale(1) rotate(0deg)`, opacity: 1 },
        { offset: fadeStart, transform: `translate(${dx}px, ${dy}px) scale(1) rotate(0deg)`, opacity: 1 },
        { transform: `translate(${dx}px, ${dy}px) scale(1.08) rotate(0deg)`, opacity: 0 },
      ],
      { duration: total, fill: "forwards" },
    ),
  )

  removeWhenDone(overlay, animations)
}

/**
 * Burst stars out from viewport coordinates (x, y), and run a row of coffee
 * cups along the bottom of the screen: in left to right, then out left to
 * right.
 */
export function burstCoffee(x: number, y: number) {
  if (prefersReducedMotion()) return
  const overlay = createOverlay(x, y)
  const animations = burstGlyphs(overlay, "star")

  const row = document.createElement("div")
  row.setAttribute("aria-hidden", "true")
  Object.assign(row.style, {
    position: "fixed",
    left: "0",
    right: "0",
    bottom: "24px",
    display: "flex",
    justifyContent: "center",
    gap: `${COFFEE_GAP_PX}px`,
    pointerEvents: "none",
    zIndex: "100",
  })
  document.body.append(row)

  // As many cups as fit across the screen, with a cup's width of margin
  const { clientWidth } = document.documentElement
  const count = Math.max(
    1,
    Math.floor((clientWidth - COFFEE_SIZE_PX * 2 + COFFEE_GAP_PX) / (COFFEE_SIZE_PX + COFFEE_GAP_PX)),
  )
  // Every cup is fully in before the first one starts fading out
  const fadeAt = count * COFFEE_STAGGER_MS + COFFEE_HOLD_MS
  const duration = fadeAt + COFFEE_OUT_MS

  for (let index = 0; index < count; index++) {
    const cup = document.createElement("i")
    cup.className = "wlicon-coffee leading-none"
    Object.assign(cup.style, {
      fontSize: `${COFFEE_SIZE_PX}px`,
      color: COLORS[index % COLORS.length],
      opacity: "0",
    })
    row.append(cup)
    animations.push(
      cup.animate(
        [
          { transform: "translateY(16px) scale(0.6)", opacity: 0, easing: "cubic-bezier(0.2, 0.9, 0.3, 1.3)" },
          { offset: COFFEE_IN_MS / duration, transform: "translateY(0) scale(1)", opacity: 1 },
          { offset: fadeAt / duration, transform: "translateY(0) scale(1)", opacity: 1 },
          { transform: "translateY(-10px) scale(0.9)", opacity: 0 },
        ],
        { duration, delay: index * COFFEE_STAGGER_MS, fill: "both" },
      ),
    )
  }

  Promise.all(animations.map((animation) => animation.finished)).finally(() => {
    overlay.remove()
    row.remove()
  })
}

// Plain stars are the usual celebration; 1 time in 6 it's a special one
// instead — an even pick between these.
const SPECIAL_CHANCE = 1 / 6
const SPECIAL_CELEBRATIONS = [burstThumbsUp, burstCoffee]

/**
 * Celebrate from viewport point (x, y): usually just the stars (5 in 6),
 * otherwise one of the special celebrations at random.
 */
export function celebrate(x: number, y: number) {
  if (Math.random() >= SPECIAL_CHANCE) {
    burstStars(x, y)
    return
  }
  const pick = SPECIAL_CELEBRATIONS[Math.floor(Math.random() * SPECIAL_CELEBRATIONS.length)]
  pick(x, y)
}
