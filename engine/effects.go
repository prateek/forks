package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/shlex"
)

type cmdResult struct {
	stdout string
	stderr string
	code   int
}

func (r cmdResult) ok() bool { return r.code == 0 }

func (r cmdResult) out() string { return strings.TrimRight(r.stdout, "\n") }

type GitExecutor struct {
	log       *slog.Logger
	logs      *logManager
	commitEnv []string
}

// PinCommitMeta pins commit metadata for deterministic assembly commits.
func (g *GitExecutor) PinCommitMeta(date string) {
	g.commitEnv = []string{
		"GIT_AUTHOR_NAME=fork-assemble",
		"GIT_AUTHOR_EMAIL=fork-assemble@invalid",
		"GIT_COMMITTER_NAME=fork-assemble",
		"GIT_COMMITTER_EMAIL=fork-assemble@invalid",
		"GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_DATE=" + date,
	}
}

func (g *GitExecutor) CommitDate(ws, sha string) (string, error) {
	res, err := g.mustGit(ws, "show", "-s", "--format=%cI", sha)
	return res.out(), err
}

func NewGitExecutor(log *slog.Logger, logs *logManager) *GitExecutor {
	return &GitExecutor{log: log, logs: logs}
}

func (g *GitExecutor) run(args ...string) cmdResult {
	return g.runEnv(nil, args...)
}

func (g *GitExecutor) must(args ...string) (cmdResult, error) {
	return g.mustEnv(nil, args...)
}

func (g *GitExecutor) runEnv(extraEnv []string, args ...string) cmdResult {
	t0 := time.Now()
	cmd := exec.Command(args[0], args[1:]...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			stderr.WriteString(err.Error())
		}
	}
	g.log.Debug("exec",
		slog.String("cmd", strings.Join(args, " ")),
		slog.Int("rc", code),
		slog.Int64("duration_ms", time.Since(t0).Milliseconds()))
	return cmdResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func (g *GitExecutor) mustEnv(extraEnv []string, args ...string) (cmdResult, error) {
	res := g.runEnv(extraEnv, args...)
	if !res.ok() {
		g.log.Error("command failed",
			slog.Int("rc", res.code),
			slog.String("cmd", strings.Join(args, " ")),
			slog.String("stdout", orEmpty(strings.TrimSpace(res.stdout))),
			slog.String("stderr", orEmpty(strings.TrimSpace(res.stderr))))
		return res, fmt.Errorf("command failed (rc=%d): %s\n%s",
			res.code, strings.Join(args, " "), strings.TrimSpace(res.stderr))
	}
	return res, nil
}

func orEmpty(s string) string {
	if s == "" {
		return "(empty)"
	}
	return s
}

func (g *GitExecutor) git(ws string, args ...string) cmdResult {
	return g.gitEnv(nil, ws, args...)
}

func (g *GitExecutor) mustGit(ws string, args ...string) (cmdResult, error) {
	return g.mustGitEnv(nil, ws, args...)
}

// hardenedGitConfig neuters the config keys a hostile resolver could use to
// turn an ordinary git invocation into arbitrary shell (hooks, fsmonitor).
// Every engine git call in the workspace carries these; the resolver's own
// git calls are outside engine control and are bounded by the resume
// contract instead.
var hardenedGitConfig = []string{
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.fsmonitor=false",
}

func (g *GitExecutor) gitEnv(extraEnv []string, ws string, args ...string) cmdResult {
	return g.runEnv(extraEnv, append(append([]string{"git", "-C", ws}, hardenedGitConfig...), args...)...)
}

func (g *GitExecutor) mustGitEnv(extraEnv []string, ws string, args ...string) (cmdResult, error) {
	return g.mustEnv(extraEnv, append(append([]string{"git", "-C", ws}, hardenedGitConfig...), args...)...)
}

const workspaceMarker = "fork-assemble-owned"

func (g *GitExecutor) PrepareWorkspace(cfg *Config, ws string) error {
	gitDir := filepath.Join(ws, ".git")
	marker := filepath.Join(gitDir, workspaceMarker)
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		g.log.Info("workspace: cloning", slog.String("url", cfg.upstreamURL), slog.String("path", ws))
		if err := os.MkdirAll(filepath.Dir(ws), 0o755); err != nil {
			return err
		}
		if _, err := g.must("git", "-c", "protocol.ext.allow=never",
			"clone", "--quiet", cfg.upstreamURL, ws); err != nil {
			return err
		}
		if err := os.WriteFile(marker, []byte("owned by downstream-fork assemble\n"), 0o644); err != nil {
			return err
		}
		g.logs.AttachWorkspace(ws)
	} else {
		if _, err := os.Stat(marker); err != nil {
			return fmt.Errorf("workspace %s is a git repo but is not owned by assemble; choose an empty --workspace or remove it yourself", ws)
		}
		g.log.Info("workspace: reusing", slog.String("path", ws))
		g.logs.AttachWorkspace(ws)
	}
	steps := [][]string{
		{"config", "protocol.ext.allow", "never"},
		{"remote", "set-url", "origin", cfg.upstreamURL},
		{"fetch", "--quiet", "origin", cfg.upstreamBranch},
		{"config", "rerere.enabled", "true"},
	}
	for _, step := range steps {
		if _, err := g.mustGit(ws, step...); err != nil {
			return err
		}
	}
	return nil
}

func (g *GitExecutor) ScrubWorkspace(ws string) error {
	g.git(ws, "merge", "--abort")
	g.git(ws, "am", "--abort")
	if err := g.ClearResolverContext(ws); err != nil {
		return err
	}
	if _, err := g.mustGit(ws, "reset", "--hard", "--quiet"); err != nil {
		return err
	}
	_, err := g.mustGit(ws, "clean", "-fdxq")
	return err
}

func snapshotConfigPath(ws string) string {
	return filepath.Join(ws, ".git", "fork-config.snapshot")
}

func snapshotIndexPath(ws string) string {
	return filepath.Join(ws, ".git", "fork-index.snapshot")
}

// ClearResolverContext drops pause-time snapshots and any hooks a resolver
// session may have planted; .git contents survive `git clean`, so they need
// explicit removal. Called once a resolution commits and at scrub time.
func (g *GitExecutor) ClearResolverContext(ws string) error {
	for _, p := range []string{snapshotConfigPath(ws), snapshotIndexPath(ws)} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	hooks := filepath.Join(ws, ".git", "hooks")
	if err := os.RemoveAll(hooks); err != nil {
		return err
	}
	return os.MkdirAll(hooks, 0o755)
}

// SnapshotResolverContext records the workspace state the resolver is allowed
// to change: .git/config byte-for-byte, and the full index. Taken at pause
// time, before any untrusted code runs.
func (g *GitExecutor) SnapshotResolverContext(ws string) error {
	cfg, err := os.ReadFile(filepath.Join(ws, ".git", "config"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(snapshotConfigPath(ws), cfg, 0o644); err != nil {
		return err
	}
	res, err := g.mustGit(ws, "ls-files", "-s")
	if err != nil {
		return err
	}
	return os.WriteFile(snapshotIndexPath(ws), []byte(res.stdout), 0o644)
}

type indexEntry struct {
	mode string
	oid  string
}

func parseIndexSnapshot(data string) map[string]indexEntry {
	stage0 := map[string]indexEntry{}
	for _, line := range strings.Split(data, "\n") {
		meta, path, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 || fields[2] != "0" {
			continue
		}
		stage0[path] = indexEntry{mode: fields[0], oid: fields[1]}
	}
	return stage0
}

// EnforceResumeContract reverts everything a resolver session changed outside
// its mandate before the engine trusts the workspace again: .git/config is
// restored from the pause-time snapshot, planted hooks are removed, tracked
// files outside the conflicted set are reset to their pause-time index state,
// and stray untracked files are cleaned. Returns the reverted paths.
func (g *GitExecutor) EnforceResumeContract(ws string, conflicts []string) ([]string, error) {
	if cfg, err := os.ReadFile(snapshotConfigPath(ws)); err == nil {
		if err := os.WriteFile(filepath.Join(ws, ".git", "config"), cfg, 0o644); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	hooks := filepath.Join(ws, ".git", "hooks")
	if err := os.RemoveAll(hooks); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		return nil, err
	}

	snapData, err := os.ReadFile(snapshotIndexPath(ws))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("resume contract: index snapshot missing; run --abort")
		}
		return nil, err
	}
	snapshot := parseIndexSnapshot(string(snapData))
	res, err := g.mustGit(ws, "ls-files", "-s")
	if err != nil {
		return nil, err
	}
	current := parseIndexSnapshot(res.stdout)

	conflicted := map[string]bool{}
	for _, f := range conflicts {
		conflicted[f] = true
	}
	reverted := map[string]bool{}
	for path, want := range snapshot {
		if conflicted[path] {
			continue
		}
		if got, ok := current[path]; !ok || got != want {
			if _, err := g.mustGit(ws, "update-index", "--add",
				"--cacheinfo", fmt.Sprintf("%s,%s,%s", want.mode, want.oid, path)); err != nil {
				return nil, err
			}
			reverted[path] = true
		}
	}
	for path := range current {
		if conflicted[path] {
			continue
		}
		if _, ok := snapshot[path]; !ok {
			if _, err := g.mustGit(ws, "rm", "--cached", "-q", "--", path); err != nil {
				return nil, err
			}
			reverted[path] = true
		}
	}
	worktree := g.git(ws, "diff", "--no-ext-diff", "--name-only").out()
	if worktree != "" {
		for _, path := range strings.Split(worktree, "\n") {
			if conflicted[path] {
				continue
			}
			if _, ok := snapshot[path]; ok {
				reverted[path] = true
			}
		}
	}
	for path := range reverted {
		if _, ok := snapshot[path]; ok {
			if _, err := g.mustGit(ws, "checkout", "-q", "--", path); err != nil {
				return nil, err
			}
		}
	}
	if _, err := g.mustGit(ws, "clean", "-fdq"); err != nil {
		return nil, err
	}
	// Snapshots are NOT removed here: a resume that then fails a later check
	// (unresolved conflicts, aborted op) will be retried, and the next attempt
	// still needs them. They are cleared when the resolution actually commits
	// (ClearResolverContext) or when the workspace is scrubbed.
	paths := make([]string, 0, len(reverted))
	for path := range reverted {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func (g *GitExecutor) UpstreamHead(ws, branch string) (string, error) {
	res, err := g.mustGit(ws, "rev-parse", "origin/"+branch)
	return res.out(), err
}

func (g *GitExecutor) CurrentHead(ws string) (string, error) {
	res, err := g.mustGit(ws, "rev-parse", "HEAD")
	return res.out(), err
}

func (g *GitExecutor) Checkout(ws, sha string) error {
	_, err := g.mustGit(ws, "checkout", "--quiet", "--detach", sha)
	return err
}

func (g *GitExecutor) FetchPRHead(ws string, n int) (string, error) {
	if _, err := g.mustGit(ws, "fetch", "--quiet", "origin",
		fmt.Sprintf("+refs/pull/%d/head:%s", n, prRef(n))); err != nil {
		return "", err
	}
	res, err := g.mustGit(ws, "rev-parse", prRef(n))
	if err != nil {
		return "", err
	}
	return res.out(), nil
}

func (g *GitExecutor) MergePR(ws string, n int, head string) OperationResult {
	return operationResult(g.gitEnv(g.commitEnv, ws,
		"merge", "--no-ff", "--no-edit", prRef(n),
		"-m", fmt.Sprintf("assemble: merge PR #%d @ %.12s", n, head)))
}

func prRef(n int) string {
	return fmt.Sprintf("refs/fork/pr-%d", n)
}

func branchRef(name string) string {
	return "refs/fork/branch/" + name
}

// BranchHead resolves a branch's tip on the fork remote without fetching it.
func (g *GitExecutor) BranchHead(ws, url, branch string) (string, error) {
	res, err := g.mustGit(ws, "ls-remote", url, "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(res.out())
	if len(fields) == 0 {
		return "", fmt.Errorf("branch %q not found on %s", branch, url)
	}
	return fields[0], nil
}

func (g *GitExecutor) FetchBranch(ws, url, branch string) (string, error) {
	if _, err := g.mustGit(ws, "fetch", "--quiet", url,
		fmt.Sprintf("+refs/heads/%s:%s", branch, branchRef(branch))); err != nil {
		return "", err
	}
	res, err := g.mustGit(ws, "rev-parse", branchRef(branch))
	if err != nil {
		return "", err
	}
	return res.out(), nil
}

func (g *GitExecutor) MergeBranch(ws, branch, head string) OperationResult {
	return operationResult(g.gitEnv(g.commitEnv, ws,
		"merge", "--no-ff", "--no-edit", branchRef(branch),
		"-m", fmt.Sprintf("assemble: merge branch %s @ %.12s", branch, head)))
}

// BranchAlreadyUpstream reports whether merging the branch would introduce
// nothing new: its diff against its merge-base with HEAD reverse-applies
// cleanly to the current (pristine-upstream) tree. Mirrors the patch check.
func (g *GitExecutor) BranchAlreadyUpstream(ws, branch string) bool {
	base := g.git(ws, "merge-base", "HEAD", branchRef(branch)).out()
	if base == "" {
		return false
	}
	diff := g.git(ws, "diff", base, branchRef(branch))
	if !diff.ok() || strings.TrimSpace(diff.stdout) == "" {
		// No diff means the branch adds nothing over the base; if the base is
		// an ancestor of HEAD the change is already present.
		return g.git(ws, "merge-base", "--is-ancestor", branchRef(branch), "HEAD").ok()
	}
	check := exec.Command("git", "-C", ws, "apply", "--reverse", "--check")
	check.Stdin = strings.NewReader(diff.stdout)
	return check.Run() == nil
}

func (g *GitExecutor) ApplyPatch(ws, path string) OperationResult {
	return operationResult(g.gitEnv(g.commitEnv, ws, "am", "-3", path))
}

// FetchRemotePatch downloads rawurl, verifies it hashes to wantSHA (a mismatch
// is a hard failure, never a silent apply), and writes it to dest. file:// is
// accepted for local rehearsal.
func FetchRemotePatch(rawurl, wantSHA, dest string) error {
	body, err := readPatchURL(rawurl)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if got != strings.ToLower(wantSHA) {
		return fmt.Errorf("remote patch %s: sha256 mismatch: pinned %s, downloaded %s", rawurl, wantSHA, got)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, body, 0o644)
}

func readPatchURL(rawurl string) ([]byte, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return nil, fmt.Errorf("remote patch %s: %w", rawurl, err)
	}
	if u.Scheme == "file" {
		return os.ReadFile(u.Path)
	}
	req, err := http.NewRequest("GET", rawurl, nil)
	if err != nil {
		return nil, err
	}
	resp, err := forgeHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote patch %s: %w", rawurl, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote patch %s: %s", rawurl, resp.Status)
	}
	return body, nil
}

func (g *GitExecutor) PatchAlreadyPresent(ws, path string) bool {
	return g.git(ws, "apply", "--reverse", "--check", path).ok()
}

func (g *GitExecutor) SkipPatch(ws string) error {
	_, err := g.mustGit(ws, "am", "--skip")
	return err
}

func (g *GitExecutor) CommitMerge(ws string) error {
	_, err := g.mustGitEnv(g.commitEnv, ws, "commit", "--no-edit")
	return err
}

func (g *GitExecutor) ContinuePatch(ws string) error {
	_, err := g.mustGitEnv(g.commitEnv, ws, "am", "--continue")
	return err
}

func (g *GitExecutor) AddResolved(ws string, paths []string) error {
	_, err := g.mustGit(ws, append([]string{"add", "--"}, paths...)...)
	return err
}

func (g *GitExecutor) ConflictedFiles(ws string) []string {
	out := g.git(ws, "diff", "--name-only", "--diff-filter=U").out()
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func (g *GitExecutor) RerereResolvedEverything(ws string) (bool, error) {
	res, err := g.mustGit(ws, "rerere", "remaining")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(res.stdout) == "", nil
}

func (g *GitExecutor) MergeInProgress(ws string) bool {
	_, err := os.Stat(filepath.Join(ws, ".git", "MERGE_HEAD"))
	return err == nil
}

func (g *GitExecutor) PatchInProgress(ws string) bool {
	fi, err := os.Stat(filepath.Join(ws, ".git", "rebase-apply"))
	return err == nil && fi.IsDir()
}

func (g *GitExecutor) MergeHead(ws string) (string, error) {
	res, err := g.mustGit(ws, "rev-parse", "MERGE_HEAD")
	if err != nil {
		return "", err
	}
	return res.out(), nil
}

func (g *GitExecutor) IsAncestor(ws, sha string) bool {
	return g.git(ws, "merge-base", "--is-ancestor", sha, "HEAD").ok()
}

func (g *GitExecutor) AbortMerge(ws string) OperationResult {
	return operationResult(g.git(ws, "merge", "--abort"))
}

func (g *GitExecutor) AbortPatch(ws string) OperationResult {
	return operationResult(g.git(ws, "am", "--abort"))
}

func (g *GitExecutor) PreloadRerere(cfg *Config, ws string) error {
	if fi, err := os.Stat(cfg.rerereDir()); err != nil || !fi.IsDir() {
		return nil
	}
	return copyTree(cfg.rerereDir(), filepath.Join(ws, ".git", "rr-cache"))
}

func (g *GitExecutor) HarvestRerere(cfg *Config, ws string) error {
	rr := filepath.Join(ws, ".git", "rr-cache")
	if fi, err := os.Stat(rr); err != nil || !fi.IsDir() {
		return nil
	}
	// Only the durable resolution pair is worth committing: rerere also
	// leaves transient bookkeeping (thisimage*) behind during replays, and
	// harvesting those makes an otherwise-identical sync commit non-empty.
	return filepath.WalkDir(rr, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := d.Name()
		if name != "preimage" && name != "postimage" {
			return nil
		}
		rel, err := filepath.Rel(rr, path)
		if err != nil {
			return err
		}
		target := filepath.Join(cfg.rerereDir(), rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func operationResult(res cmdResult) OperationResult {
	return OperationResult{
		OK:      res.ok(),
		Explain: strings.TrimSpace(strings.TrimSpace(res.stdout) + "\n" + strings.TrimSpace(res.stderr)),
	}
}

// ForgeClient resolves the PR heads that should be assembled.
type ForgeClient interface {
	ResolvePRSet(cfg *Config) (map[string]string, error)
}

type cliForgeClient struct {
	argv []string
	repo string
	git  *GitExecutor
	log  *slog.Logger
}

type giteaForgeClient struct {
	rest *giteaREST
	repo string
	log  *slog.Logger
}

type prInfo struct {
	State string `json:"state"`
	Head  string `json:"headRefOid"`
}

const forgePRListLimit = 1000

func NewForgeClient(cfg *Config, git *GitExecutor, log *slog.Logger) (ForgeClient, error) {
	cli := os.Getenv("FORGE_CLI")
	if cli == "" {
		if u, err := url.Parse(cfg.upstreamURL); err == nil &&
			(u.Scheme == "http" || u.Scheme == "https") && u.Host != "github.com" {
			base := strings.TrimSuffix(cfg.upstreamURL, "/"+cfg.upstreamRepo+".git")
			return &giteaForgeClient{
				rest: &giteaREST{base: base, token: os.Getenv("FORGE_TOKEN")},
				repo: cfg.upstreamRepo, log: log,
			}, nil
		}
		cli = "gh"
	}
	argv, err := shlex.Split(cli)
	if err != nil || len(argv) == 0 {
		return nil, fmt.Errorf("FORGE_CLI unparseable: %q", cli)
	}
	return &cliForgeClient{argv: argv, repo: cfg.upstreamRepo, git: git, log: log}, nil
}

type giteaREST struct {
	base  string
	token string
}

type giteaPR struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Merged bool   `json:"merged"`
	Head   struct {
		SHA string `json:"sha"`
	} `json:"head"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (pr giteaPR) info() prInfo {
	state := strings.ToUpper(pr.State)
	if pr.Merged {
		state = "MERGED"
	}
	return prInfo{State: state, Head: pr.Head.SHA}
}

var forgeHTTPClient = &http.Client{Timeout: 30 * time.Second}

func (r *giteaREST) get(path string, into any) error {
	req, err := http.NewRequest("GET", r.base+"/api/v1/"+path, nil)
	if err != nil {
		return err
	}
	if r.token != "" {
		req.Header.Set("Authorization", "token "+r.token)
	}
	resp, err := forgeHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, into)
}

func (r *giteaREST) listPRs(repo, state string) ([]giteaPR, error) {
	query := url.Values{
		"state": {state},
		"limit": {strconv.Itoa(forgePRListLimit)},
	}
	var prs []giteaPR
	if err := r.get(fmt.Sprintf("repos/%s/pulls?%s", repo, query.Encode()), &prs); err != nil {
		return nil, err
	}
	return prs, nil
}

func (f *cliForgeClient) forgeJSON(dst any, args ...string) error {
	argv := append(append([]string(nil), f.argv...), args...)
	r, err := f.git.must(argv...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(r.stdout), dst)
}

func (f *cliForgeClient) prView(number int) (prInfo, error) {
	var info prInfo
	err := f.forgeJSON(&info, "pr", "view", strconv.Itoa(number),
		"--repo", f.repo, "--json", "state,headRefOid")
	return info, err
}

func (f *cliForgeClient) openPRHeadsByAuthor(author string) (map[string]string, error) {
	var items []struct {
		Number int    `json:"number"`
		Head   string `json:"headRefOid"`
	}
	if err := f.forgeJSON(&items, "pr", "list", "--repo", f.repo,
		"--author", author, "--state", "open", "--limit", strconv.Itoa(forgePRListLimit),
		"--json", "number,headRefOid"); err != nil {
		return nil, err
	}
	heads := map[string]string{}
	for _, item := range items {
		heads[strconv.Itoa(item.Number)] = item.Head
	}
	return heads, nil
}

func (f *cliForgeClient) ResolvePRSet(cfg *Config) (map[string]string, error) {
	if cfg.hasTrack {
		candidates := append([]int(nil), cfg.track...)
		f.log.Info("PR set: tracking explicit list", slog.Any("prs", candidates))
		return resolveTrackedPRSet(f.log, candidates, f.prView)
	}

	heads, err := f.openPRHeadsByAuthor(cfg.author)
	if err != nil {
		return nil, err
	}
	candidates := prNumbers(heads)
	f.log.Info("PR set: author discovery", slog.String("author", cfg.author), slog.Any("prs", candidates))
	if err := logOpenPRHeads(f.log, candidates, heads); err != nil {
		return nil, err
	}
	return heads, nil
}

func (f *giteaForgeClient) ResolvePRSet(cfg *Config) (map[string]string, error) {
	if cfg.hasTrack {
		candidates := append([]int(nil), cfg.track...)
		f.log.Info("PR set: tracking explicit list", slog.Any("prs", candidates))
		prs, err := f.rest.listPRs(f.repo, "all")
		if err != nil {
			return nil, err
		}
		byNumber := map[int]prInfo{}
		for _, pr := range prs {
			byNumber[pr.Number] = pr.info()
		}
		return resolveTrackedPRSet(f.log, candidates, func(n int) (prInfo, error) {
			info, ok := byNumber[n]
			if !ok {
				return prInfo{}, fmt.Errorf("PR #%d not returned by forge list", n)
			}
			return info, nil
		})
	}

	prs, err := f.rest.listPRs(f.repo, "open")
	if err != nil {
		return nil, err
	}
	heads := map[string]string{}
	for _, pr := range prs {
		if pr.User.Login == cfg.author {
			heads[strconv.Itoa(pr.Number)] = pr.Head.SHA
		}
	}
	candidates := prNumbers(heads)
	f.log.Info("PR set: author discovery", slog.String("author", cfg.author), slog.Any("prs", candidates))
	if err := logOpenPRHeads(f.log, candidates, heads); err != nil {
		return nil, err
	}
	return heads, nil
}

func resolveTrackedPRSet(log *slog.Logger, candidates []int, view func(int) (prInfo, error)) (map[string]string, error) {
	sort.Ints(candidates)
	heads := map[string]string{}
	for _, n := range candidates {
		info, err := view(n)
		if err != nil {
			return nil, err
		}
		recordPRInfo(log, heads, n, info)
	}
	return heads, nil
}

func recordPRInfo(log *slog.Logger, heads map[string]string, n int, info prInfo) {
	state := strings.ToUpper(info.State)
	if state == "OPEN" {
		heads[strconv.Itoa(n)] = info.Head
		log.Info("PR open", slog.Int("pr", n), slog.String("head", info.Head))
		return
	}
	log.Info("PR not open", slog.Int("pr", n), slog.String("state", state))
}

func logOpenPRHeads(log *slog.Logger, candidates []int, heads map[string]string) error {
	for _, n := range candidates {
		head := heads[strconv.Itoa(n)]
		if head == "" {
			return fmt.Errorf("PR #%d missing headRefOid from forge list", n)
		}
		log.Info("PR open", slog.Int("pr", n), slog.String("head", head))
	}
	return nil
}
