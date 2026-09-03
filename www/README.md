# worklode.io

The marketing site for Worklode. Pre-built static HTML — no build step, no
dependencies. The directory is published as-is.

| File | Purpose |
|---|---|
| `index.html` | The whole site: one page, seven sections |
| `styles.css` | All styling. Light theme only, by design |
| `app.js` | Reveal-on-scroll only; the page is fully readable without it |
| `logo.svg` | The mark — nav, hero, footer and tab icon. Not square: 1340×1150 |
| `logo-512.png` | Square raster of the mark; the source every icon below is cut from |
| `favicon.ico` | Tab-icon fallback for browsers that ignore SVG icons — 48/32/16 |
| `apple-touch-icon.png` | 180×180 home-screen icon for iOS, white background |
| `CNAME` | Custom domain for GitHub Pages — do not delete |
| `CLAUDE.md` (`AGENTS.md` symlinks to it) | Accuracy rules for agents editing this directory's copy; the language style itself lives in the root `CLAUDE.md` |

The one external dependency is the analytics tag in `index.html`, pointing at
Sunstone's self-hosted Umami instance at `sunstone.institute/umami`. It is
deferred, so it never blocks rendering, and the page works with it blocked.

## Preview locally

```bash
python3 -m http.server 8000 --directory www
```

Then open <http://localhost:8000/>.

## Deployment

`.github/workflows/deploy-www.yml` publishes this directory to GitHub Pages on
every push to `main` that touches `www/`, and can be run by hand from the
Actions tab. `CNAME` sets the custom domain; GitHub reads it on each deploy, so
it must stay in this directory.

One-time setup outside this repo:

1. **Repo settings → Pages → Source: GitHub Actions.**
2. DNS — `opentofu/live/prod/cloudflare/domain-worklode-io.tf` in the
   `provisioning` repo points the apex at GitHub Pages' anycast addresses.
   The records are deliberately **DNS-only** (not proxied): GitHub validates
   the apex directly to issue the custom-domain certificate, and behind the
   Cloudflare proxy that fails and "Enforce HTTPS" stays greyed out.
3. Once DNS resolves, tick **Enforce HTTPS** in the repo's Pages settings.

PR checks skip `www/`-only changes — the site shares no code with the Go build.
Apply the `can-be-tested` label to force a full run.

## Content

The copy was derived from the umbrella spec, which has since been removed
(`lode doc list --kind spec` is the map). The two-store split and
the ambition-reconciliation thesis are stated in `WL-SPEC-6` (knowledge graph);
the layer model in `WL-SPEC-7` (drift and overview); the document lifecycle
in `WL-SPEC-25` (documents in the backbone). When the architecture changes
materially, update this copy too — nothing derives it automatically.

The Escalation section (`#ladder`) describes 025 §8, which is fully specified
but not yet implemented — no `lode task escalate`, no fixer subagent, no
gap-tracking events exist in code today. Its copy is deliberately future-tense
("will record", "will amend") to say so; when §8 ships, update the tense along
with whatever the shipped behaviour actually does.

## Logo

`logo.svg` is the mark: a faceted lodestone crystal between two magnetic field
lines. Its viewBox is 1340×1150, so it is *not* square — set `width` and
`height` at a 1.165:1 ratio (35×30 in the nav, 30×26 in the footer) or it
distorts. It is also the tab icon in every browser that supports SVG icons.

### Icons

`favicon.ico` and `apple-touch-icon.png` are cut from `logo-512.png` with
ImageMagick. Regenerate both after changing the mark:

```bash
cd www
for s in 48 32 16; do
  magick logo-512.png -crop 306x442+103+36 +repage -background none \
    -gravity center -extent 470x470 -filter Lanczos -resize ${s}x${s} /tmp/i$s.png
done
magick /tmp/i48.png /tmp/i32.png /tmp/i16.png favicon.ico
magick logo-512.png -resize 148x148 -background white -alpha remove -alpha off \
  -gravity center -extent 180x180 apple-touch-icon.png
```

The `.ico` crops to the **crystal alone**. At 16 px the field lines eat the
frame and the whole mark reduces to a blob; cropping them away buys back enough
pixels for the crystal to read. The apple-touch icon keeps the full mark — it
renders at 180 px, where nothing is lost — on white, because iOS composites
transparency onto black.

### Regenerating the mark

The prompts below are written for Gemini "Nano Banana 2" and produced the
current mark. Generate at high resolution, then trace to SVG — ship vector,
since the mark renders at 26–30 px in the nav and footer.

**Prompt A — the mark, on its own**

> A minimalist flat vector logo mark for a developer tool called Worklode. The
> subject is a lodestone: a faceted hexagonal magnetite crystal, drawn as clean
> geometric facets with two or three internal edge lines suggesting depth.
> Two symmetrical curved magnetic field lines arc around it, left and right, as
> if drawing scattered iron filings into alignment. Colour: a smooth gradient
> from deep blue #1D4ED8 to sky blue #38BDF8, with thin white facet lines. Pure
> white background. Centred, generous margin, no text, no drop shadow, no
> gloss, no 3D bevel. Crisp hard edges, flat design, high contrast, legible when
> scaled down to 32 pixels.

**Prompt B — horizontal lockup with wordmark**

> A horizontal logo lockup for a developer tool. On the left, a small flat
> geometric mark: a faceted hexagonal lodestone crystal in a deep-blue-to-sky
> gradient (#1D4ED8 → #38BDF8) with thin white facet lines, flanked by two
> symmetrical curved magnetic field lines. To its right, the single word
> "Worklode" in a modern geometric sans-serif, medium weight, tight letter
> spacing, near-black #0F172A. Pure white background, generous margin, mark and
> text optically aligned on a shared baseline. Flat vector style, no shadow, no
> 3D, no tagline.

**Prompt C — alternative concept, alignment-first**

> A minimalist flat vector logo mark. A small dark blue faceted stone sits at
> the centre. Around it, twelve short straight dashes are arranged in a loose
> ring — the dashes nearest the stone point directly at it in crisp alignment,
> while the dashes furthest away sit at scattered random angles, showing order
> emerging from disorder. Deep blue #1D4ED8 for the stone, sky blue #38BDF8 for
> the aligned dashes, light grey #CBD5E1 for the scattered ones. Pure white
> background, centred, no text, flat design, no shadow, geometric and precise,
> legible at small sizes.

If the model returns a white background rather than transparency, drop the
background out afterwards — the mark sits on both white and light-grey bands.
Keep the palette to the site's blues so the header mark and the page agree.
