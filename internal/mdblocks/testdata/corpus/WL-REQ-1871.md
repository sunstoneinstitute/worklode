
Worklode Markdown is the set of Markdown constructs the body editor converts
to blocks one to one. Each entry names the construct, the spelling
Worklode stores, and the block it becomes. Markdown that Worklode stores
renders unmodified in a Quarto pipeline, so where Worklode's renderer and
Quarto spell a construct differently, the stored spelling is Quarto's and
`internal/mdrender` reads both.

| Construct | Stored spelling | Block |
|---|---|---|
| Paragraph | one line of text | `paragraph` |
| Heading | `## Title`, ATX only | `heading` |
| Heading attributes | `## Title {#id}`, document and rule bodies only | `heading` with an `anchor` prop |
| Bullet list item | `- item` | `bulletListItem` |
| Numbered list item | `1. item` | `numberedListItem` |
| Task list item | `- [ ] item`, `- [x] item` | `checkListItem` |
| Pipe table | GFM pipe table | `table` |
| Fenced code | ```` ```lang ```` | `codeBlock` |
| Mermaid diagram | ```` ```{mermaid} ```` | `codeBlock` with language `mermaid` |
| Block quote | `> text` | `quote` |
| Callout | `::: {.callout-note}` … `:::`; also `warning`, `important`, `tip`, `caution` | `callout` |
| Image | `![alt](src)` | `image` |
| Inline emphasis, strong, strikethrough, code, link | `*x*`, `**x**`, `~~x~~`, `` `x` ``, `[x](url)` | text styles and links in `content` |
| Emoji | the Unicode character; a `:shortcode:` is written as its character | text |

Any other construct becomes a `source` block. Examples: raw HTML; an
angle-bracket placeholder in prose, such as `<id>` in `GET /docs/<id>`, which
Markdown parses as an HTML tag; indented code; and a multi-line inline code
span. A `source` block holds its Markdown
verbatim and is edited as plain text.

Checks: a fixture with one instance of every entry renders through Quarto
with no warnings and with every construct present in the output.
