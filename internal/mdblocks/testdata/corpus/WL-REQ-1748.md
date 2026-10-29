
`scripts/check-vendored-skills.py` compares each vendored skill against the upstream revision in its frontmatter and reports what changed upstream. `--check` exits non-zero for CI and pre-commit; default mode reports only. It never rewrites. Upstream sources are read from the local plugin cache; an uncached revision is reported as unverifiable and exits zero.


