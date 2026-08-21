package skillsync

import (
	"encoding/json"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/skillhash"
)

// A skill's registry identity is <plugin>:<name> (037 §4). The name half comes
// from SKILL.md; this file derives the plugin half, which is what keeps two
// plugins shipping the same skill name from colliding in the registry.

// manifestDir is where a Claude plugin declares itself, relative to the
// plugin's own root.
const manifestDir = ".claude-plugin"

// pluginManifestRoot reports whether parts names a plugin manifest, and if so
// the plugin's root dir — the dir holding .claude-plugin. A manifest at the
// repo root yields "", which qualifierFor treats as matching every skill.
func pluginManifestRoot(parts []string) (string, bool) {
	n := len(parts)
	if n < 2 || parts[n-2] != manifestDir || parts[n-1] != "plugin.json" {
		return "", false
	}
	return strings.Join(parts[:n-2], "/"), true
}

// pluginName reads the "name" a plugin manifest declares, or "" when the blob
// is not usable as one. A qualifier may not itself contain a colon: the
// qualified name is split on its first one, so a colon in the plugin half
// would make the identity unparseable.
func pluginName(blob []byte) string {
	var m struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(blob, &m); err != nil {
		return ""
	}
	if !validQualifier(m.Name) {
		return ""
	}
	return m.Name
}

func validQualifier(q string) bool {
	return skillhash.ValidName(q) && !strings.Contains(q, ":")
}

// qualifierFor picks the plugin that ships the skill at dir: the name in the
// nearest plugin manifest above it, matched by longest prefix.
//
// Not the source repo, because a source is owner/repo@ref:glob and one repo
// holds many plugins — the scanner's normal case is a glob like
// plugins/*/skills/*, which is how a marketplace repo publishes a dozen.
// Deriving from the repo would give every plugin in it the same qualifier,
// which is the collision again under a longer name.
//
// Not SKILL.md frontmatter, because the qualifier is a namespace claim: a
// SKILL.md that could name its own plugin could claim another plugin's.
//
// A repo of bare skills has no manifest above the skill dir. There the
// qualifier is the source repo's last path segment, which keeps every synced
// skill qualified without requiring plugin packaging.
func (sy *Syncer) qualifierFor(src Source, manifests map[string]string, dir string) string {
	best, found := "", false
	for root := range manifests {
		if !underRoot(dir, root) {
			continue
		}
		if !found || len(root) > len(best) {
			best, found = root, true
		}
	}
	if found {
		return manifests[best]
	}
	return repoQualifier(src.Repo)
}

// underRoot reports whether dir is the plugin root or sits beneath it. The
// empty root is a manifest at the repo root, which covers every skill in it.
func underRoot(dir, root string) bool {
	return root == "" || dir == root || strings.HasPrefix(dir, root+"/")
}

// repoQualifier is the fallback qualifier: the source repo's last path
// segment. A repo whose name is unusable as a qualifier (it can hold "." or
// start with one, which a skill name may not) is sanitised rather than
// dropped — an unqualifiable repo must not cost every skill in it its
// registry entry.
func repoQualifier(repo string) string {
	seg := repo
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	seg = strings.ReplaceAll(seg, ":", "-")
	seg = strings.TrimLeft(seg, ".")
	if !validQualifier(seg) {
		return "skills"
	}
	return seg
}
