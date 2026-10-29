
`internal/mdrender` provides `Slides(keys ProjectKeys, body string) ([]Slide, error)` beside `Body` and `DocBody`. A `Slide` carries its layout name, parsed front matter, and content as sanitised `template.HTML`. `Slides` splits on top-level `---`, parses each slide's front matter as raw text, and renders the rest through the same goldmark instance and bluemonday policy as `Body`, auto-links included. `internal/ui` holds layouts and components; `internal/api` serves the page.

