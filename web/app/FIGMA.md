# FIGMA.md

How pages/components get imported from Figma into this codebase, and the
decisions specific to this Figma file. See `AGENTS.md` for the general
project workflow (design-token system, icon build pipeline, static HTML
export for the Go backend).

## Source file

`Worklode-Wireframes`, key `Wq95LQEArB69q0jTCnnvq1`. Confirmed frames:

- Desktop homepage: node `344:4150` ("Home Main" — NOT the sibling "Home"
  frame `344:3980`, which is an older/abandoned layout)
- Mobile homepage: node `435:1242` ("Home Mobile")
- Desktop Project Overview: node `387:3348` ("Project Overview Desktop")
- Mobile Project Overview: node `462:1420` ("Project Overview Mobile" — this
  frame didn't exist yet when the homepage was built; the user added it
  between sessions)
- Mobile menu: node `435:1243` ("Mobile Components" > "Menu Mobile"), variants
  `Property 1=Default` (`435:1320`, closed), `Property 1=Open` (`435:1322`,
  Home's menu), `Property 1=Open Projects` (`462:1690`, Project Overview's
  menu — adds a "project folder" card reusing the exact same nav items as
  the desktop `ProjectToolbar`, see `@/lib/project-nav`). A fourth variant,
  `Property 1=Variant4` (`462:1813`), wasn't used — it's a small sub-piece
  (385×203) not part of the top-level open/closed states.

Home and Project Overview (desktop + mobile, including the mobile menu) are
built. The file also has frames for Project Documents, Tasks, and a
Confirmation dialog that aren't implemented yet.

## Workflow for importing a new page/component

1. Load the `figma-design-to-code` skill (via the Figma MCP server's
   `skill://figma/figma-design-to-code/SKILL.md` resource) before calling
   `get_design_context` — it's a hard prerequisite, not optional.
2. `get_metadata` first to find the right node IDs. Figma files often have
   duplicate/abandoned frames with similar names (see "Home" vs "Home Main"
   above) — confirm with the user which one is current before pulling full
   design context, rather than guessing.
3. `get_design_context` on the target frame returns reference React+Tailwind
   code — treat it as a reference to adapt, not final code. Reuse this
   project's existing components/tokens instead of what it invents.
4. **Design tokens are the source of truth, not one-off hex values pulled
   from a random frame.** Use `get_variable_defs` against the actual target
   node to get the real Figma Variables. Early on we mistakenly pulled
   colors from an unrelated "Blue and orange theme study" frame that just
   happened to share similar values — the user explicitly corrected this.
   Only use values confirmed via `get_variable_defs` on the real frame.
5. **Dark mode values aren't resolvable via the Figma API for this file** —
   there's no node with an explicit dark-mode variable override, and
   `get_variable_defs` only ever resolves whichever single mode is active
   file-wide (confirmed early on by querying the ColorModeToggle's
   `DarkMode` vs `LightMode` variant nodes directly — both returned
   identical values). **This is resolved, not a live gap**: the user
   exported the actual "Worklode Theme" variables table from Figma
   (Light/Dark columns for every token) and shared it directly as a
   screenshot, which is what `src/index.css`'s `.dark` block is built from
   — see AGENTS.md's Design tokens section. If a *new* token needs a
   dark-mode value that isn't in that table, ask for it the same way rather
   than guessing or inventing one from an unrelated frame.
6. Icons in this Figma file are Code-Connect-mapped to Material icon names,
   but Code Connect data itself wasn't queryable here (`get_code_connect_map`
   requires a Dev/Full seat on an Org/Enterprise plan this file isn't on) —
   icon names were matched by hand from Figma layer names instead. The user
   confirmed the icon style used in Figma is **Material Symbols, weight 300,
   filled** — see `AGENTS.md`'s Icons section for how that's actually built
   into this codebase.
7. A large decorative/illustrated icon (an alarm/clock graphic in the
   "Upcoming Meetings" rows) was explicitly left as a placeholder — the user
   is still designing it. Don't try to reproduce bespoke illustrated icons
   pixel-for-pixel; ask before investing effort there.

## How Figma Variables map into code

Every Figma Variable used gets named 1:1 as a CSS custom property in
`src/index.css` (`--color-text-primary`, `--color-surface`,
`--color-highlight`, etc.) — kept traceable back to the Figma file by name.
shadcn's generic slots (`--background`, `--primary`, `--border`, ...) just
point at these; don't bypass this and hardcode a shadcn slot directly from a
Figma hex value. When a new page introduces a Figma Variable not yet in
`src/index.css`, add it the same way rather than inlining its hex value.

## Layout decisions specific to this design

- Desktop page margin is 70px, mobile is 20px (`--spacing-page` in
  `src/index.css`) — this applies to page *content*, not the header.
- The header is a deliberate **exception**: it always uses 20px padding
  regardless of breakpoint (not 70px on desktop), so the "Worklode" wordmark's
  left edge lines up with the page content below it while the logo mark
  itself sits further left, closer to the edge. This was a direct
  back-and-forth with the user after noticing the logo mark and body text
  didn't align — worth re-checking on any future page that has its own
  header-adjacent lockup.
- The desktop "Upcoming Meetings" sidebar is also an exception: the panel
  bleeds flush to the viewport's right edge and starts immediately below the
  header (no outer grid padding on that side), while its own *inner*
  content keeps the standard 70px margin via `pr-page`. Figma's own
  "Rectangle 17" background for this panel is drawn flush to the frame's
  right edge (x + width == frame width), confirming this is intentional in
  the source file, not a rendering accident. Currently an 8/4 column split
  (`lg:grid-cols-12`), fluid rather than a fixed pixel sidebar width — the
  user asked for 3/9 fluid first, then changed to 8/4 after seeing it.
- Mobile "Project Quick Links" cards bleed past the page's 20px margin and
  scroll horizontally, matching the Figma mobile frame's cards being drawn
  wider than the mobile viewport (a horizontal-scroll intent, not an
  oversight in the design).
- The secondary sticky toolbar (Figma's "Project Toolbar" component,
  instance `387:3351` on Project Overview Desktop) is now built as
  `ProjectToolbar.tsx` — see `AGENTS.md` for the sticky-positioning gotcha
  hit while wiring it up, and the CSS driving it. It's desktop-only: the
  mobile frame (`462:1420`) has no toolbar in its metadata tree at all.
- Project Overview's desktop sidebar bleeds to the right edge the same way
  Home's does, but does **not** start flush below the header/toolbar like
  Home's does — Figma positions its background level with the main
  column's own content (i.e. after the page's 70px top margin), not the
  header's bottom edge. Handled with `lg:mt-page` on the sidebar rather
  than `lg:items-stretch` starting it at the row's very top.
- Project Overview's mobile layout order differs from Home Mobile's: the
  project summary card comes first, then a reduced (2-item, vs. desktop's
  3) Upcoming Meetings band, then the task list — Home Mobile instead opens
  with Upcoming Meetings in a hero band. Always check the actual frame's
  child order rather than assuming a new page mirrors an existing one's
  mobile layout.
- The Project Toolbar's icon nav (Overview/Tasks/Meetings/Documents/
  Reviews/Activity/settings) needed 6 new icons not yet in the icon set —
  matched by hand to Material Symbols names (`dashboard`, `event`,
  `description`, `rate_review`, `history`, `settings`) since Code Connect
  wasn't queryable here either (same limitation as the original icon set).
- The toolbar's decorative white "FolderCurve" shape (curving from behind
  the breadcrumb into the gradient) was initially left out as a bespoke
  illustrated flourish — the user corrected this; unlike the Upcoming
  Meetings clock illustration, this one was wanted. The user supplied the
  actual asset (`src/assets/brand/folder-curve.svg`). It went through a few
  iterations: an absolutely-positioned fixed size overlapped the nav at
  narrower widths; a flex-1 box with `object-cover` fixed that but then
  didn't cover the breadcrumb area at all (the breadcrumb sits on the
  gradient's own lighter starting color, not a light background); the
  current version is a plain `bg-background` rectangle covering the
  breadcrumb area (matching the SVG's own flat fill color) plus the actual
  image fixed at `w-64` anchored to the nav-facing edge for the curve
  itself — see AGENTS.md for why the rectangle can't extend under the image
  (it hides the curve's transparent cutout).
- The Project Summary's "Past Due Tasks" and "Awaiting Review" lists stack
  vertically in a single right-hand column (matching Figma's actual
  metadata structure — they were initially built side-by-side by mistake).
  The CTA button aligns with "Past Due Tasks" (the top of that column) via
  `justify-between` on a height-matched row, not a fixed offset — this is
  how Figma's own layout ties them together (both start at the same y
  within their shared parent frame).
- The Upcoming Meetings placeholder clock (both pages, shared component)
  renders the filled "schedule" glyph directly, larger — the glyph is
  already circular, so the separate circle-behind-icon wrapper was
  redundant and looked like two overlapping circles.

- The mobile menu (`MobileMenu.tsx`) reuses Figma's exact link set per
  variant: "Open" has plain links only; "Open Projects" adds the folder card
  between "Projects" and "Tasks", and drops the plain links to 80% opacity
  (matching Figma) to keep focus on the card. Figma positions everything via
  absolute coordinates within a fixed 970px-tall panel; this was adapted to
  a natural flowing flex layout (`justify-center`-style gap-based spacing)
  instead of replicating exact pixel offsets, consistent with how other
  absolutely-positioned Figma exports have been adapted throughout this
  project — the visual proportions match closely without needing pixel-exact
  positions that wouldn't hold up responsively anyway.
- Two deliberate divergences from Figma's mobile menu, both explicit user
  requests: the plain links render smaller (`text-xl`, not Figma's 24px) and
  the folder card's internal padding is tighter than spec, both because the
  content didn't comfortably fit as originally sized; and the avatar in
  Header stays visible while the menu is open, even though Figma's
  "Open"/"Open Projects" variants drop it from that row.
- The folder card's own mini "curve" header strip initially reused the
  desktop `ProjectToolbar`'s solid-rectangle + narrow-anchored-image split,
  but cropped far too narrowly (a ~64px sliver of an image meant to be
  viewed much wider) — at that scale the crop looked like an abrupt skewed
  diagonal rather than a smooth curve, not an actual distortion (object-fit
  never skews), just a bad crop window. Fixed by cropping to the strip's
  *full* width instead, and dropping the separate solid-rectangle backing
  entirely — this container's own `bg-primary/10` tint is what Figma
  intends to show through the curve's transparent cutout here (unlike the
  desktop toolbar, whose backdrop isn't tinted, hence the rectangle there).
  If you reuse this curve treatment somewhere new, crop width matters more
  than it might seem — err wide.
- The curve source SVG (`folder-curve.svg`) is a plain white rectangle for
  ~82% of its width, with the actual S-curve confined to its rightmost ~18%
  — the rest is flat, not rounded. Narrowing the displayed image (e.g. from
  `w-full` to `w-11/12`, still anchored `object-right`) shifts the crop
  window left into that flat white area, which sounds harmless but exposes
  the *container's* own tint on the newly-uncovered left strip instead of
  the image's white fill — since the container has no rounded corner baked
  in at that exact spot, this reads as the folder's corner being "cut off"
  rather than as a narrower curve. There's no way to make the visible curve
  narrower via this crop-and-anchor approach without this side effect: any
  width less than `w-full` uncovers real container background on the left.
  Keep this image at `w-full`.

## Known Figma-side gaps

- The decorative "schedule" icon (Upcoming Meetings) is a bespoke
  illustration still being designed by the user — left as a plain
  placeholder circle+glyph, not a faithful reproduction.
