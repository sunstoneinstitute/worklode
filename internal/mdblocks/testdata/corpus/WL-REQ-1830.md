
`project_resolve(spoken)` matches a spoken name against projects only, by fuzzy and sound-alike matching, and returns ranked candidates with a confidence. It never selects one, not even a single perfect match. Claude reads back the full name, or the top two or three, and waits for a yes before calling `project_use(confirmed=true)`.

