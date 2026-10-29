
An editor block is one unit of the body editor's document in BlockNote's model: a JSON
object with an `id`, a `type` (`paragraph`, `heading`, `codeBlock` and so on),
type-specific `props`, inline `content` and nested `children`. Its shape is
the editor block type in `internal/model`, which admits only the block types
Worklode Markdown lists.

Editor blocks exist only between `lode-server` and the SPA while a Markdown field is
being edited. Stored Markdown contains no blocks, and a block's `id` is minted
when the field is converted and is not stable across loads.
