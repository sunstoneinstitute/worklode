
A skill's registry identity is `<plugin>:<name>`, for example `lode:assay`, `superpowers:brainstorming`. `skillhash.ValidName` permits `:`. The qualifier is the `name` in the nearest `.claude-plugin/plugin.json` above the skill directory. It comes from neither the source repo (one repo can hold many plugins) nor SKILL.md frontmatter (a skill could claim another plugin's namespace). A repo of bare skills with no manifest uses the source repo's last path segment.

`skillsync`'s tarball walk collects manifests anywhere in the tree, keyed by directory, and matches each skill dir by longest prefix. The manifest stays out of the archive, so it does not enter `content_hash`. `skills_qualified_name_unique` is unique on `(qualifier, name)`.

