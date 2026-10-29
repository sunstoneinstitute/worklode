
The source format is Slidev's: slides separated by `---`, each optionally opening with its own front matter block. The first block is the body's own front matter; every later block belongs to the slide that follows. A `doc`-mode reader sees a document with thematic breaks.

Layouts are code and content is data. The sanitiser strips `class` globally, so markdown cannot carry layout. Layouts are `templ` components in `internal/ui` holding every class and grid; markdown fills their slots after the normal render-and-sanitise pass. A slide's front matter picks a layout by name and passes a small typed set of values. An unknown layout renders as `statement` and is reported by `lode doctor`.

