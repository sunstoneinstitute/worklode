
Not built (see "Designed, not built").

`internal/store` readers feed `assembleProjectCockpit`, which extends
`model.CockpitProjection` with the four panel shapes; `render.go` maps them onto
`ui.CockpitView`. Planning outcome is computed by `designdoc.Todo`, shared with
`lode doc todo` through an exported `[]model.Doc` to `[]designdoc.CorpusDoc`
conversion in `internal/designdoc`, so the page and the CLI cannot disagree.

