
The SPA body editor's conversion between Markdown and blocks runs in
`lode-server`, in Go, in both directions. The SPA never parses or writes
Markdown. The editor asks for blocks with the query parameter `as=blocks` on
the subject's `GET` (`/api/v1/docs/{id}`, `/api/v1/rules/{id}`,
`/api/v1/tasks/{id}`): the response then carries the body as `blocks` and
`body_sha256`, the hex SHA-256 of the stored Markdown body the blocks came
from. A body write carries either `body` (Markdown) or `blocks`, never both;
a write carrying both, or a block of a type Worklode Markdown does not list,
is refused with `422`. A blocks write is converted to Markdown and then takes
the Markdown write path.

The conversion maps every Worklode Markdown construct to its block, and any
other construct to a `source` block holding its Markdown verbatim.

Every write of a document, rule or task body normalises it, from an agent or
from the editor: the stored text is what converting the input to blocks and back
produces. Converting stored Markdown to blocks and back is then the identity,
so editing one block never rewrites text the person did not touch. The writer
puts each paragraph and each list item on one line and never wraps. Accepted
rule and document versions are locked and keep their text; normalisation
applies to the next write. The other Markdown fields WL-RULE-1850 lists
(titles, notes, idea bodies and the rest) are not edited as blocks and are
not normalised.

The editor block type is declared once in `internal/model`, like every
shape that crosses the API, so it appears in the OpenAPI document and the
SPA's generated types. Its JSON shape is written to match BlockNote at the
pinned version, and the contract test is what proves it does.

Each block carries the UTF-8 byte range of the stored Markdown it came from,
and each inline text run carries the byte offset of its first character, so a
text selection in the editor maps to a byte range in the stored Markdown and
back.

Checks: a test converts every rule and task body in a corpus fixture to blocks
and back and requires the second round trip to equal the first. Fixture
documents prove each listed out-of-profile construct becomes a `source` block.
A test maps every character of a fixture's blocks to a byte offset and back. A
contract test loads the Go-produced blocks into headless BlockNote at the
pinned version and requires the same JSON back.
