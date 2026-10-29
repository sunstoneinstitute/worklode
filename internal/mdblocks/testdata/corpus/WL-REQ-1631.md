
Opening a candidate revision copies the document's live outgoing edges into `doc_revision_edges`. While the candidate is open, an edge write on the document goes to the candidate's set, through the same resolve-and-validate path live edges use, so a bad reference is refused when it is written. `lode doc revise --accept` bumps the version, which snapshots the live edges, then swaps in the candidate's set and deletes the candidate. Discarding the candidate deletes its edges with it.

