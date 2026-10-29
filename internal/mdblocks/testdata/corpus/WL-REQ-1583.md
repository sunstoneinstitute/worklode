
Branch names are rendered by the server from `LODE_BRANCH_TEMPLATE` (default `{{ .id }}-{{ .slug }}`), and `store.TaskIDFromRef` derives its pattern from that template. Worktrees live at `<worktree_dir>/<branch>` (default `.worktrees/<id>-<slug>`). The template grammar and worktree naming are in WL-REQ-267.
