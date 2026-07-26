# worklode.io

The marketing site for Worklode. Pre-built static HTML — no build step, no
dependencies. The directory is published as-is.

| File | Purpose |
|---|---|
| `index.html` | The whole site: one page, seven sections |
| `styles.css` | All styling. Light theme only, by design |
| `app.js` | Reveal-on-scroll only; the page is fully readable without it |
| `logo.svg` | Mark used in the header and footer |
| `favicon.svg` | Browser-tab icon |
| `CNAME` | Custom domain for GitHub Pages — do not delete |

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

The copy is derived from `docs/specs/000-umbrella-architecture.md`. When the
architecture changes materially — the two-store split, the three layers, the
drift model — update both.

## Logo

`logo.svg` is a hand-drawn placeholder: a faceted lodestone crystal between two
magnetic field lines. It is real SVG and scales cleanly, so it is fine to ship
as-is.

To generate a richer mark with an image model, the prompts below are written for
Gemini "Nano Banana 2". Generate at high resolution, then trace to SVG (or keep
a PNG at 2× and add `logo.png` alongside) — ship vector for the header, since
the mark renders at 26–30 px.

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
