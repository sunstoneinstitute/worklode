
Containment never crosses a project. Two references do: `dependsOn` between tasks, and a milestone depending on a deliverable produced under another project. Plus `seeded_by`. References are rows in one typed edge table `(from_kind, from_id, to_kind, to_id, rel)`. There is no unified entities table.

