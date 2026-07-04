package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type lockFile struct {
	UpstreamSHA   string            `json:"upstream_sha"`
	AssembledHEAD string            `json:"assembled_head,omitempty"`
	PRHeads       map[string]string `json:"pr_heads"`
	BranchHeads   map[string]string `json:"branch_heads,omitempty"`
	Patches       map[string]string `json:"patches"`
}

type manifestUpdate struct {
	notes    []string
	warnings []string
}

func loadLock(cfg *Config) (*lockFile, error) {
	data, err := os.ReadFile(cfg.lockPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var l lockFile
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("%s: %w", cfg.lockPath(), err)
	}
	return &l, nil
}

func writeLock(cfg *Config, upstreamSHA, assembledHead string, prHeads, branchHeads, digest map[string]string) error {
	return writeJSONFile(cfg.lockPath(), lockFile{
		UpstreamSHA:   upstreamSHA,
		AssembledHEAD: assembledHead,
		PRHeads:       prHeads,
		BranchHeads:   branchHeads,
		Patches:       digest,
	})
}

func explainDrift(lock *lockFile, newSHA string, prHeads, branchHeads, digest map[string]string, workspaceHead string) []string {
	if lock == nil {
		return []string{"no lock.json — first sync"}
	}
	var reasons []string
	if lock.UpstreamSHA != newSHA {
		reasons = append(reasons, fmt.Sprintf("upstream moved %.12s -> %.12s", lock.UpstreamSHA, newSHA))
	}
	switch {
	case lock.AssembledHEAD == "":
		reasons = append(reasons, "lock.json is missing assembled_head")
	case lock.AssembledHEAD != workspaceHead:
		reasons = append(reasons, fmt.Sprintf("workspace moved %.12s -> %.12s", lock.AssembledHEAD, workspaceHead))
	}
	for _, n := range sortedKeysByInt(union(lock.PRHeads, prHeads)) {
		old, new_ := lock.PRHeads[n], prHeads[n]
		switch {
		case old == new_:
		case old == "":
			reasons = append(reasons, fmt.Sprintf("PR #%s newly tracked at %.12s", n, new_))
		case new_ == "":
			reasons = append(reasons, fmt.Sprintf("PR #%s left the open set", n))
		default:
			reasons = append(reasons, fmt.Sprintf("PR #%s head moved %.12s -> %.12s", n, old, new_))
		}
	}
	for _, name := range sortedKeys(union(lock.BranchHeads, branchHeads)) {
		old, new_ := lock.BranchHeads[name], branchHeads[name]
		switch {
		case old == new_:
		case old == "":
			reasons = append(reasons, fmt.Sprintf("branch %s newly tracked at %.12s", name, new_))
		case new_ == "":
			reasons = append(reasons, fmt.Sprintf("branch %s no longer tracked", name))
		default:
			reasons = append(reasons, fmt.Sprintf("branch %s head moved %.12s -> %.12s", name, old, new_))
		}
	}
	for _, name := range sortedKeys(union(lock.Patches, digest)) {
		old, new_ := lock.Patches[name], digest[name]
		switch {
		case old == new_:
		case old == "":
			reasons = append(reasons, fmt.Sprintf("patch %s added", name))
		case new_ == "":
			reasons = append(reasons, fmt.Sprintf("patch %s removed", name))
		default:
			reasons = append(reasons, fmt.Sprintf("patch %s content changed", name))
		}
	}
	return reasons
}

func union(a, b map[string]string) map[string]struct{} {
	keys := map[string]struct{}{}
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	return keys
}

func sortedKeys(keys map[string]struct{}) []string {
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeysByInt(keys map[string]struct{}) []string {
	out := sortedKeys(keys)
	sort.Slice(out, func(i, j int) bool {
		a, _ := atoiSafe(out[i])
		b, _ := atoiSafe(out[j])
		return a < b
	})
	return out
}

func atoiSafe(s string) (int, bool) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

var (
	tableHeaderRe = regexp.MustCompile(`(?m)^\[`)
	shaLineRe     = regexp.MustCompile(`(?m)^(\s*sha\s*=\s*")[^"]*(")`)
	trackLineRe   = regexp.MustCompile(`(?m)^(\s*track\s*=\s*)\[[^\]]*\]`)
)

func tableSpan(text, table string) (int, int, bool) {
	header := regexp.MustCompile(`(?m)^\[` + regexp.QuoteMeta(table) + `\]\s*$`).FindStringIndex(text)
	if header == nil {
		return 0, 0, false
	}
	start := header[1]
	if next := tableHeaderRe.FindStringIndex(text[start:]); next != nil {
		return start, start + next[0], true
	}
	return start, len(text), true
}

func bumpManifest(cfg *Config, newSHA string, openPRs []int, keptBranches, droppedPatches, droppedBranches []string) (manifestUpdate, error) {
	raw, err := os.ReadFile(cfg.tomlPath())
	if err != nil {
		return manifestUpdate{}, err
	}
	text, update, err := updateManifestText(string(raw), cfg, newSHA, openPRs, keptBranches, droppedBranches)
	if err != nil {
		return manifestUpdate{}, err
	}
	// A remote patch is dropped by removing its [[patches.remote]] entry; a
	// local patch is dropped by deleting its file. Do the manifest edits (both
	// track pruning and remote-entry removal) before the single atomic write.
	for _, name := range droppedPatches {
		if !isRemotePatchName(name) {
			continue
		}
		sha := remoteSHAFor(cfg, name)
		next, removed := removeRemotePatchEntry(text, sha)
		if !removed {
			update.warnings = append(update.warnings,
				"could not remove already-upstream remote patch entry for "+name)
			continue
		}
		text = next
		update.notes = append(update.notes, "dropped already-upstream remote patch: "+name)
	}
	if err := atomicWriteFile(cfg.tomlPath(), []byte(text), 0o644); err != nil {
		return manifestUpdate{}, err
	}
	for _, name := range droppedPatches {
		if isRemotePatchName(name) {
			continue
		}
		if err := os.Remove(filepath.Join(cfg.patchesDir(), name)); err != nil {
			return manifestUpdate{}, err
		}
		update.notes = append(update.notes, "dropped already-upstream patch: "+name)
	}
	return update, nil
}

func remoteSHAFor(cfg *Config, name string) string {
	for _, rp := range cfg.remotePatches {
		if remotePatchName(rp.SHA256) == name {
			return rp.SHA256
		}
	}
	return ""
}

// removeRemotePatchEntry deletes the single [[patches.remote]] array-of-tables
// block whose sha256 matches, preserving the rest of the file byte-for-byte.
func removeRemotePatchEntry(text, sha string) (string, bool) {
	if sha == "" {
		return text, false
	}
	blockRe := regexp.MustCompile(`(?m)^\[\[patches\.remote\]\]\s*$`)
	locs := blockRe.FindAllStringIndex(text, -1)
	for _, loc := range locs {
		bodyStart := loc[1]
		end := len(text)
		if next := tableHeaderRe.FindStringIndex(text[bodyStart:]); next != nil {
			end = bodyStart + next[0]
		}
		if strings.Contains(strings.ToLower(text[bodyStart:end]), strings.ToLower(sha)) {
			// Take the block from its header line through the trailing blank
			// lines so removal leaves no double gap.
			start := loc[0]
			for start > 0 && text[start-1] == '\n' {
				start--
				if start > 0 && text[start-1] == '\n' {
					break
				}
			}
			return text[:start] + strings.TrimLeft(text[end:], "\n"), true
		}
	}
	return text, false
}

func updateManifestText(text string, cfg *Config, newSHA string, openPRs []int, keptBranches, droppedBranches []string) (string, manifestUpdate, error) {
	start, end, found := tableSpan(text, "upstream")
	if !found {
		return "", manifestUpdate{}, fmt.Errorf("fork.toml: missing [upstream] table")
	}
	span := text[start:end]
	if shaLineRe.MatchString(span) {
		span = shaLineRe.ReplaceAllString(span, "${1}"+newSHA+"${2}")
	} else {
		span = "\nsha = \"" + newSHA + "\"" + span
	}
	text = text[:start] + span + text[end:]

	var update manifestUpdate
	if cfg.hasTrack && !equalIntSets(cfg.track, openPRs) {
		if start, end, found = tableSpan(text, "prs"); found && trackLineRe.MatchString(text[start:end]) {
			pruned := make([]string, len(openPRs))
			for i, n := range openPRs {
				pruned[i] = fmt.Sprintf("%d", n)
			}
			span = trackLineRe.ReplaceAllString(text[start:end],
				"${1}["+strings.Join(pruned, ", ")+"]")
			text = text[:start] + span + text[end:]
			update.notes = append(update.notes, "dropped merged/closed PRs: "+
				joinPRRefs(setDifference(cfg.track, openPRs)))
		} else {
			update.warnings = append(update.warnings,
				"could not prune track list: no `track = [...]` line inside [prs]")
		}
	}
	if cfg.hasBranches && len(droppedBranches) > 0 {
		if start, end, found = tableSpan(text, "branches"); found && trackLineRe.MatchString(text[start:end]) {
			quoted := make([]string, len(keptBranches))
			for i, b := range keptBranches {
				quoted[i] = fmt.Sprintf("%q", b)
			}
			span = trackLineRe.ReplaceAllString(text[start:end],
				"${1}["+strings.Join(quoted, ", ")+"]")
			text = text[:start] + span + text[end:]
			update.notes = append(update.notes, "dropped already-upstream branches: "+
				strings.Join(droppedBranches, ", "))
		} else {
			update.warnings = append(update.warnings,
				"could not prune branch track list: no `track = [...]` line inside [branches]")
		}
	}
	return text, update, nil
}

func equalIntSets(a, b []int) bool {
	as, bs := append([]int(nil), a...), append([]int(nil), b...)
	sort.Ints(as)
	sort.Ints(bs)
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func setDifference(a, b []int) []int {
	inB := map[int]bool{}
	for _, n := range b {
		inB[n] = true
	}
	var out []int
	for _, n := range a {
		if !inB[n] {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}
