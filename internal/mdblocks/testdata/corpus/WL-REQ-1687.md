
| Layout | Holds |
|---|---|
| `cover` | deck title, subtitle, one full-bleed image |
| `section` | a numbered divider |
| `figure` | one headline number, a caption, a source line, an optional proportion bar |
| `statement` | a heading and prose; the fallback |
| `list` | an ordered or unordered list, optionally with expandable detail per item |
| `decisions` | the task's decision rows as answerable rows (WL-REQ-214) |
| `close` | a closing image and short text |

`image:` on a layout names a blob by its `/blob/<hash>` reference (WL-REQ-225), so deck art uses the existing blob store, rewriting and garbage collection. The brand supergraphic ovals are drawn by the layouts, never supplied as images. The set is closed. Adding a layout changes this catalogue.

