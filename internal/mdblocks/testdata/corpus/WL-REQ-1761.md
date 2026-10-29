
`./scripts/narrow-check.sh` renders every page component with its fixtures in a
headless browser at 320, 375 and 768 CSS px and 200% zoom. CI installs no
browser, so what runs unattended is the set of markup and stylesheet facts each
finding was fixed by. The settled rules:

- No page scrolls horizontally at 320 CSS px. A data table scrolls inside its
  own labelled, keyboard-focusable container (the one exception WCAG 1.4.10
  grants).
- Nothing is truncated to fit. A row that cannot hold its content stacks.
- Visual order follows document order at every width. The decision rail
  follows the work list on a phone.
- A fixed bar reserves scroll room both at the end of the document and on
  scroll-into-view (2.4.11).
- An identifier (artifact address, branch name, table reference) has no soft
  wrap opportunity, so its box breaks anywhere.
- Muted text `--ink-3` clears 4.5:1 against the page background, the least
  contrasty surface it reaches.
- A control's outline carries its own token at 3:1 (1.4.11). Divider and
  card-edge tokens are decorative and exempt.
- One `:focus-visible` outline, drawn 2px clear of the focused box.
- A rejected form submit is a new document, so the validation message takes
  focus on load and the page title is prefixed `Error:`.
- The light theme's accent is the brand hue darkened toward amber until fill
  and border clear 3:1 on every surface and its ink clears 4.5:1. The dark
  theme keeps the brand value.

1.3.1, 1.3.2, 1.4.3, 1.4.4, 1.4.10, 1.4.11, 1.4.12, 2.4.7, 2.4.11, 2.5.8 and
4.1.2 are verified against the built pages with no outstanding exception.

