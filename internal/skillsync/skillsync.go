// Package skillsync ingests skill directories from configured git source
// repos into the backbone: parse SKILL.md frontmatter, content-hash the dir,
// archive it, and (when a provider is configured) embed the SKILL.md body.
package skillsync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"sort"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/skillhash"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Size caps, as vars so tests can lower them instead of building the real
// thing. maxSkillBytes caps one skill dir: 8 MiB leaves room for a skill that
// ships images or scripts alongside its prose. maxSourceBytes caps the
// decompressed content retained from one source — the 64 MiB download cap
// bounds the *compressed* tarball only, so without this a highly compressible
// tree could hold far more than that in memory at once.
// maxSkillEntries mirrors the client-side extract cap: a dir the server
// happily ingests but no client will unpack is a skill nobody can install.
var (
	maxSkillBytes   = 8 << 20
	maxSourceBytes  = 64 << 20
	maxSkillEntries = skillhash.MaxEntries
)

// FetchFunc downloads a repo tarball at a ref (githubauth.AppAuth.Tarball).
type FetchFunc func(ctx context.Context, repo, ref string) ([]byte, error)

type Syncer struct {
	Store *store.Store
	Fetch FetchFunc
	Embed embed.Provider // nil = pins-only instance, skip embedding
	Log   *slog.Logger   // nil = slog.Default()
}

type Summary struct {
	Synced   int `json:"synced"`  // skills found and upserted
	Changed  int `json:"changed"` // of those, new content this sync
	Deleted  int `json:"deleted"`
	Embedded int `json:"embedded"` // embedded by the convergence pass
}

func (sy *Syncer) log() *slog.Logger {
	if sy.Log != nil {
		return sy.Log
	}
	return slog.Default()
}

// SyncAll fully syncs every source: upsert found skills, soft-delete the
// missing, re-embed the changed, and embed whatever still has no vectors.
// A failing source does not stop the others — a 5xx or a rate limit on one
// repo should not leave the rest unsynced — so the returned Summary covers
// whatever did sync alongside the joined errors. A failed embedding
// invalidation is the exception: it returns before any source is synced, so
// a zero Summary with an error means nothing happened at all.
func (sy *Syncer) SyncAll(ctx context.Context, sources []Source) (Summary, error) {
	var sum Summary
	var errs []error
	if sy.Embed != nil {
		// Before anything is embedded: syncing first would write vectors of the
		// new model into a table still holding the old model's, which mixes two
		// incomparable spaces and, on a dimension change, breaks every query.
		if err := InvalidateOnProviderChange(ctx, sy.Store, sy.Embed, sy.Log); err != nil {
			return sum, err
		}
	}
	for _, src := range sources {
		s, err := sy.syncSource(ctx, src)
		sum.Synced += s.Synced
		sum.Changed += s.Changed
		sum.Deleted += s.Deleted
		if err != nil {
			errs = append(errs, fmt.Errorf("sync %s@%s: %w", src.Repo, src.Ref, err))
		}
	}
	if sy.Embed != nil {
		n, err := sy.embedMissing(ctx)
		sum.Embedded = n
		if err != nil {
			errs = append(errs, fmt.Errorf("converge embeddings: %w", err))
		}
	}
	return sum, errors.Join(errs...)
}

// InvalidateOnProviderChange drops every stored vector when p is not the
// provider they were computed with, and records p as the new one. Re-embedding
// on content change alone cannot recover from a swap: a skill whose content
// did not change is never re-embedded, and vectors from two models are not
// comparable — at two dimensions they make every query error outright.
//
// It takes a Store and a Provider rather than a Syncer because it must also
// run at boot on an instance with no skill sources configured, where there is
// no Syncer. A caller may pass a nil log.
func InvalidateOnProviderChange(ctx context.Context, st *store.Store, p embed.Provider, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	id := p.ID()
	stored, err := st.EmbeddingProviderID(ctx)
	if err != nil {
		return err
	}
	if stored == id {
		return nil
	}
	n, err := st.ClearAllSkillEmbeddings(ctx)
	if err != nil {
		return err
	}
	if err := st.SetEmbeddingProviderID(ctx, id); err != nil {
		return err
	}
	// Silent only on the usual first boot: no id recorded and nothing stored.
	// A real swap that finds zero vectors still gets logged — that is exactly
	// the state an operator debugging empty recommendations needs to see.
	if stored != "" || n > 0 {
		log.Info("embedding provider changed, cleared embeddings",
			"from", stored, "to", id, "cleared", n)
	}
	return nil
}

// embedMissing embeds every live skill that has no vectors, returning how
// many it embedded. This is what makes the corpus converge: it re-embeds
// after an invalidation, and retries skills whose embed call failed on an
// earlier sync — those keep their content hash, so nothing else would.
func (sy *Syncer) embedMissing(ctx context.Context) (int, error) {
	skills, err := sy.Store.SkillsMissingEmbeddings(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sk := range skills {
		if err := sy.embedSkill(ctx, sk.ID, sk.Description, sk.SkillMD); err != nil {
			sy.log().Warn("skill embed failed", "repo", sk.SourceRepo, "skill", sk.Name, "err", err)
			continue
		}
		n++
	}
	return n, nil
}

// syncSource aborts on a tarball-level failure (fetch, gunzip, read) because
// nothing about the source is then trustworthy. A failure confined to one
// skill is logged and skipped so the rest of the source still syncs.
func (sy *Syncer) syncSource(ctx context.Context, src Source) (Summary, error) {
	var sum Summary
	tb, err := sy.Fetch(ctx, src.Repo, src.Ref)
	if err != nil {
		return sum, err
	}
	dirs, commit, err := sy.skillDirs(tb, src)
	if err != nil {
		return sum, err
	}
	var seen []string
	from := map[string]string{} // skill name -> dir that already claimed it
	for _, d := range dirs {
		u, err := buildUpsert(src, commit, d.dir, d.files)
		if err != nil {
			sy.warn(src, "skipping skill dir", "dir", d.dir, "err", err)
			continue
		}
		id, changed, err := sy.Store.UpsertSkill(ctx, *u)
		if err != nil {
			sy.warn(src, "skill upsert failed", "skill", u.Name, "dir", d.dir, "err", err)
			continue
		}
		// Two dirs declaring one name collide in the registry, last write
		// winning. Only the skill authors can fix that, so just report it.
		if prev, dup := from[u.Name]; dup {
			sy.warn(src, "duplicate skill name", "skill", u.Name, "first", prev, "second", d.dir)
		}
		from[u.Name] = d.dir
		seen = append(seen, u.Name)
		sum.Synced++
		if changed {
			sum.Changed++
			if sy.Embed != nil {
				if err := sy.reembed(ctx, id, u.Description, u.SkillMD); err != nil {
					sy.warn(src, "skill embed failed", "skill", u.Name, "err", err)
				}
			}
		}
	}
	if len(dirs) == 0 {
		// The glob matched nothing, so every skill from this repo is about to
		// be soft-deleted. That is correct when a source really was emptied and
		// a disaster when the glob has a typo, and the two are indistinguishable
		// from here — so say so loudly rather than reporting a clean sync.
		sy.warn(src, "skill source matched no dirs", "glob", src.Glob, "deleting", sy.liveCount(ctx, src.Repo))
	}
	n, err := sy.Store.SoftDeleteSkillsExcept(ctx, src.Repo, seen)
	if err != nil {
		return sum, err
	}
	sum.Deleted = int(n)
	sy.log().Info("synced skill source", "repo", src.Repo, "ref", src.Ref, "commit", commit,
		"dirs", len(dirs), "synced", sum.Synced, "changed", sum.Changed, "deleted", sum.Deleted)
	return sum, nil
}

// warn logs against a source, so a multi-source install can tell which repo a
// complaint came from.
func (sy *Syncer) warn(src Source, msg string, args ...any) {
	sy.log().Warn(msg, append([]any{"repo", src.Repo, "ref", src.Ref}, args...)...)
}

// liveCount reports how many live skills the repo has, for the zero-match
// warning, or -1 if that could not be read. It runs only on the zero-match
// path, and a failure there is not worth failing the sync over.
func (sy *Syncer) liveCount(ctx context.Context, repo string) int {
	skills, err := sy.Store.ListSkills(ctx, false)
	if err != nil {
		return -1
	}
	n := 0
	for _, sk := range skills {
		if sk.SourceRepo == repo {
			n++
		}
	}
	return n
}

// reembed replaces the vectors of a skill whose content just changed. The
// old vectors go first, so a provider failure leaves the skill with none and
// embedMissing retries it later in this same SyncAll pass. Embedding in place
// instead would, on failure, leave vectors describing content the skill no
// longer has — and its content hash now matches, so nothing would ever
// replace them. Missing vectors are repairable; stale ones are invisible.
func (sy *Syncer) reembed(ctx context.Context, skillID int64, description, skillMD string) error {
	if err := sy.Store.ReplaceSkillEmbeddings(ctx, skillID, nil); err != nil {
		return err
	}
	return sy.embedSkill(ctx, skillID, description, skillMD)
}

func (sy *Syncer) embedSkill(ctx context.Context, skillID int64, description, skillMD string) error {
	chunks := embed.Chunks(description+"\n\n"+skillMD, embed.ChunkRunes, embed.ChunkOverlap)
	vecs, err := sy.Embed.Embed(ctx, chunks)
	if err != nil {
		return err
	}
	return sy.Store.ReplaceSkillEmbeddings(ctx, skillID, vecs)
}

// file is one file inside a skill dir. exec carries the executable bit, which
// skills need for the scripts they ship.
type file struct {
	data []byte
	exec bool
}

type skillDir struct {
	dir   string
	files map[string]file // paths relative to dir
	bytes int             // total retained content bytes
	over  bool            // exceeded maxSkillBytes; files dropped
}

// sortedDirs returns the dirs ordered by path, for deterministic logging and
// upsert order. Map iteration is randomized, so this cannot be skipped.
func sortedDirs(m map[string]*skillDir) []*skillDir {
	out := make([]*skillDir, 0, len(m))
	for _, d := range m {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dir < out[j].dir })
	return out
}

// skillDirs walks the tarball and groups files by the skill dir (the glob
// match) that owns them, ordered by path. Also extracts the commit sha from
// the root dir name ("owner-repo-<sha>/").
func (sy *Syncer) skillDirs(tgz []byte, src Source) ([]*skillDir, string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, "", fmt.Errorf("gunzip tarball: %w", err)
	}
	tr := tar.NewReader(gz)
	segs := strings.Count(src.Glob, "/") + 1
	dirs := map[string]*skillDir{}
	total := 0
	var commit string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("read tarball: %w", err)
		}
		root, rel, ok := strings.Cut(path.Clean(h.Name), "/")
		if !ok || rel == "" {
			continue
		}
		if commit == "" {
			if i := strings.LastIndex(root, "-"); i >= 0 {
				commit = root[i+1:]
			}
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		parts := strings.Split(rel, "/")
		if len(parts) <= segs { // file directly at or above skill-dir depth
			continue
		}
		dir := strings.Join(parts[:segs], "/")
		if ok, _ := path.Match(src.Glob, dir); !ok {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			// Symlinks are the common case here, and extracting one onto an
			// agent's machine would write outside the skill dir. Dropping is
			// right; dropping in silence is what let a skill ship a reference
			// to a file that never arrived with it.
			sy.warn(src, "skipping non-regular entry", "dir", dir, "path", rel, "type", string(h.Typeflag))
			continue
		}
		d := dirs[dir]
		if d == nil {
			d = &skillDir{dir: dir, files: map[string]file{}}
			dirs[dir] = d
		}
		if d.over {
			continue
		}
		// tar reports each entry's size up front, so an over-size dir is
		// detected and released before its bytes are ever held. "bytes" is the
		// dir's running total through this file, not its full size.
		if int64(d.bytes)+h.Size > int64(maxSkillBytes) {
			sy.warn(src, "skipping over-size skill dir", "dir", dir, "file", rel,
				"bytes", int64(d.bytes)+h.Size, "max", maxSkillBytes)
			total -= d.bytes
			d.over, d.files, d.bytes = true, nil, 0
			continue
		}
		if len(d.files) >= maxSkillEntries {
			sy.warn(src, "skipping skill dir with too many files", "dir", dir, "file", rel,
				"entries", len(d.files)+1, "max", maxSkillEntries)
			total -= d.bytes
			d.over, d.files, d.bytes = true, nil, 0
			continue
		}
		content, err := io.ReadAll(io.LimitReader(tr, h.Size))
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", h.Name, err)
		}
		d.files[strings.Join(parts[segs:], "/")] = file{data: content, exec: h.Mode&0o111 != 0}
		d.bytes += len(content)
		total += len(content)
		if total > maxSourceBytes {
			return nil, "", fmt.Errorf("skill dirs exceed %d bytes", maxSourceBytes)
		}
	}
	// A dir with no SKILL.md is not a skill. That is expected under a glob
	// like plugins/*/skills/*, so it is dropped silently — unlike the
	// over-size case above, which is a misconfiguration worth reporting.
	out := make([]*skillDir, 0, len(dirs))
	for _, d := range sortedDirs(dirs) {
		if d.over {
			continue
		}
		if _, ok := d.files["SKILL.md"]; !ok {
			continue
		}
		out = append(out, d)
	}
	return out, commit, nil
}

func buildUpsert(src Source, commit, dir string, files map[string]file) (*store.SkillUpsert, error) {
	md := string(files["SKILL.md"].data)
	name, description := parseFrontmatter(md)
	if name == "" {
		return nil, fmt.Errorf("SKILL.md has no frontmatter name")
	}
	// The same predicate the client applies at extract time. A name with a
	// separator or a leading dot would otherwise be stored and listed, then
	// rejected by every install and unroutable on GET /api/v1/skills/{name}.
	if !skillhash.ValidName(name) {
		return nil, fmt.Errorf("frontmatter name %q is not a usable skill name", name)
	}
	fm, err := json.Marshal(map[string]string{"name": name, "description": description})
	if err != nil {
		return nil, err
	}
	archive, err := buildArchive(files)
	if err != nil {
		return nil, err
	}
	return &store.SkillUpsert{
		Name: name, Description: description,
		SourceRepo: src.Repo, SourcePath: dir,
		GitCommit: commit, ContentHash: contentHash(files),
		SkillMD: md, Frontmatter: fm, Archive: archive,
	}, nil
}

// contentHash delegates to skillhash.Sum — see internal/skillhash for the
// encoding. Kept as a thin wrapper so the rest of this file stays in terms
// of the local file/map[string]file shapes.
func contentHash(files map[string]file) string {
	hfiles := make([]skillhash.File, 0, len(files))
	for p, f := range files {
		hfiles = append(hfiles, skillhash.File{Path: p, Data: f.data, Exec: f.exec})
	}
	return skillhash.Sum(hfiles)
}

// fileMode is the one mode bit that survives a sync: skills ship scripts, and
// an archive extracted without the executable bit fails at run time.
func fileMode(f file) int64 {
	return skillhash.Mode(f.exec)
}

// buildArchive produces a deterministic tar.gz: sorted paths, mode from the
// executable bit alone, zero timestamps.
func buildArchive(files map[string]file) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, p := range sortedPaths(files) {
		f := files[p]
		if err := tw.WriteHeader(&tar.Header{
			Name: p, Mode: fileMode(f), Size: int64(len(f.data)), Typeflag: tar.TypeReg,
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sortedPaths(files map[string]file) []string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// parseFrontmatter extracts name and description from a leading "---" YAML
// block. Deliberately not a YAML parser, and no YAML dependency: it reads
// top-level "key: value" scalars plus block scalars (">" folded, "|" literal,
// with any chomping indicator), which is what the SKILL.md convention uses.
// Anything else — nested mappings, flow collections, anchors — is ignored.
//
// Only column-0 keys within the frontmatter block are read, and a block
// scalar's indented lines are consumed as its value, so prose inside a
// description can never be mistaken for another key.
func parseFrontmatter(md string) (name, description string) {
	lines := frontmatterLines(md)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue // blank, or indented under a key we did not read
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		if style, isBlock := blockStyle(val); isBlock {
			val, i = blockValue(lines, i+1, style)
		} else {
			val = unquote(val)
		}
		switch strings.TrimSpace(key) {
		case "name":
			name = val
		case "description":
			description = val
		}
	}
	return name, description
}

// frontmatterLines returns the lines between the opening and closing "---",
// or nil when md has no closed frontmatter block. Delimiting the block up
// front keeps an unterminated one from turning the whole document body into
// candidate keys. A leading BOM and CRLF line endings are both tolerated: an
// editor's choice must not cost a skill its entire registry entry. Only this
// parse sees the normalized text — SkillMD, the archive, and the content hash
// all come from the file bytes.
func frontmatterLines(md string) []string {
	md = strings.ReplaceAll(strings.TrimPrefix(md, "\ufeff"), "\r\n", "\n")
	lines := strings.Split(md, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}
	lines = lines[1:]
	// The closing marker sits at column 0; an indented "---" is a block
	// scalar's content.
	for i, l := range lines {
		if strings.TrimRight(l, " \t") == "---" {
			return lines[:i]
		}
	}
	return nil
}

// unquote strips one matched pair of surrounding quotes. Trimming each end
// independently would turn `Ask "why"` into `Ask "why`.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// blockStyle reports a value's block-scalar style: '>' folded or '|' literal.
// Chomping indicators ("-", "+") are accepted and ignored — a trailing
// newline is trimmed either way.
func blockStyle(val string) (byte, bool) {
	if val == "" || (val[0] != '>' && val[0] != '|') {
		return 0, false
	}
	if strings.TrimRight(val[1:], "-+") != "" {
		return 0, false
	}
	return val[0], true
}

// blockValue consumes the indented lines of a block scalar starting at
// lines[start] and returns the value with the index of its last line. A
// folded block joins its lines with spaces (paragraph breaks are not
// preserved); a literal block keeps its line breaks and relative indent.
func blockValue(lines []string, start int, style byte) (string, int) {
	var block []string
	i := start
	for ; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			block = append(block, "")
			continue
		}
		if l[0] != ' ' && l[0] != '\t' {
			break // back at column 0: the block ended
		}
		block = append(block, l)
	}
	if style == '>' {
		var parts []string
		for _, l := range block {
			if s := strings.TrimSpace(l); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " "), i - 1
	}
	return strings.TrimRight(strings.Join(stripIndent(block), "\n"), "\n"), i - 1
}

// stripIndent removes the block's common leading indent from every line.
func stripIndent(block []string) []string {
	indent := -1
	for _, l := range block {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if n := len(l) - len(strings.TrimLeft(l, " \t")); indent < 0 || n < indent {
			indent = n
		}
	}
	out := make([]string, 0, len(block))
	for _, l := range block {
		if indent > 0 && len(l) >= indent {
			l = l[indent:]
		} else {
			l = strings.TrimLeft(l, " \t")
		}
		out = append(out, l)
	}
	return out
}
