
| Requirement | Expression |
|---|---|
| Partial implementation | `<component> wl:implements <section>`, coverage is a count |
| Section superseded, heading kept | its rule is `withdrawn` and `<successor> dct:replaces <rule>` |
| Section retired without a successor | its rule is `withdrawn` with no successor, reported by `--bare-superseded` |
| A rule leaves a spec only through `lode rule unarrange` or `lode rule supersede` | no other write removes an arrangement entry (WL-REQ-167) |
| Partial supersession of a document | `dct:replaces` between rules |

No `wl:fullyImplements` or `wl:partiallyImplements` exists. Declared coverage goes stale. Derived coverage cannot.

