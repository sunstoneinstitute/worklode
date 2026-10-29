
A classification whose predicate is not `references` writes that predicate
into its typed table at once, with `classification_id` set to the row, and its
outcome starts as `kept`. The ordinary edge writers (`lode task block`,
`lode rule link`, `lode doc link` and the rest) record a person's repair on
the row:

- changing a classified edge's predicate sets `adjusted` with the new
  predicate, and clears the edge's `classification_id`;
- deleting a classified edge sets `rejected`;
- writing a typed edge for a pair whose newest row says `references` sets that
  row to `missed`.

Re-running with `--reclassify` replaces a typed edge only while it still
carries `classification_id`, so an edge a person repaired is never
overwritten.
