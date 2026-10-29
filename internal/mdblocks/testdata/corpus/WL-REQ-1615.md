
The CLI writes the declarations of WL-REQ-133 through the API fields of WL-SPEC-76#sec-1.3a:

| Command | Behaviour |
|---|---|
| `lode task edit <id> --deliverable <ref>` | repeatable, replaces the list |
| `lode task edit <id> --deliverable-inherit` | sets NULL |
| `lode task edit <id> --deliverable-none` | sets the empty list |
| `lode project set default-deliverables <ref>...` | replaces the list |

