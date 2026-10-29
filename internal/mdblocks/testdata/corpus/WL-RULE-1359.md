
- Decomposition: a document is decomposed when every requirement it arranges is covered or deferred; `lode doc decompose <ref>` validates and stamps `wl:decomposedAt` or names the unclaimed sections; a new rule clears the stamp; accepting a plan whose governing spec is not decomposed is refused; `--needs-planning` splits `planned` into fully and partially planned, and `lode doc todo` gains an `undecomposed` item. WL-PLAN-123.
- Intents (§5): the `serves:` key, the intent class and the intent queries. WL-970.
- Decision decks (§6): `body_mode`, the deck renderer, layouts and the component registry. WL-971.
- Meetings, minutes and notes (§7): the entities, Calendar and Meet integration, and their surfaces. WL-972.
