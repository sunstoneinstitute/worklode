
The SPA's body editor lets people edit Worklode's Markdown fields in
BlockNote while Markdown stays the only stored form of those fields. Agents,
the API, search indexing and version history read and write the same Markdown
people edit, and BlockNote's block JSON is never stored.

Agents read and write document, rule and task bodies as Markdown, and in many
projects they write most of them. Block JSON is BlockNote's internal shape and
changes between BlockNote releases. Review comments anchor to byte ranges of
the stored text, which needs a stored text that BlockNote does not rewrite.
