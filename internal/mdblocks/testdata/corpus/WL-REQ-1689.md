
reveal.js (MIT) is vendored at a pinned version as two static files in `internal/ui/assets/` (`reveal.js`, 117 KB; `reveal.css`, 54 KB), served from `/assets/` with no build step. No reveal theme is used. The deck stylesheet sits on `reveal.css` in `app.css` with the Sunstone palette (Dark Blue `#0E1937`, Logo Orange Tint `#FDE6D3`, Logo Yellow `#FAD604`, Cool Grey Light `#F4F4F4`), DM Sans for UI and body, Source Serif 4 for headings, both self-hosted. A deck ignores `prefers-color-scheme` and `data-theme`. The canvas is 1280×720; type sizes are absolute within it.

