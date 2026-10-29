
`GET /api/v1/skills/{name}` accepts a qualified name. A bare name matching exactly one skill resolves. A bare name matching more than one is an ambiguity error naming the candidates, in the shape `designdoc.AmbiguousRefError` uses. Task pins and a plan's `skills:` key store what was written and resolve through the same rules; they are not rewritten. `Store.ResolveSkillRefs` counts `worklode_skill_name_ambiguous_total`.

