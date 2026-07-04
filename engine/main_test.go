package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"assemble": run,
	}))
}

func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata",
		Setup: func(env *testscript.Env) error {
			gcfg := filepath.Join(env.WorkDir, ".gitconfig-global")
			cfg := "[user]\n\tname = Rehearsal\n\temail = rehearsal@example.invalid\n" +
				"[init]\n\tdefaultBranch = main\n"
			if err := os.WriteFile(gcfg, []byte(cfg), 0o644); err != nil {
				return err
			}
			env.Setenv("GIT_CONFIG_GLOBAL", gcfg)
			env.Setenv("GIT_CONFIG_NOSYSTEM", "1")

			fixtures := filepath.Join(env.WorkDir, ".forge-fixtures")
			if err := os.MkdirAll(fixtures, 0o755); err != nil {
				return err
			}
			env.Setenv("FORGE_FIXTURES", fixtures)

			stub := filepath.Join(env.WorkDir, ".forge-stub")
			script := "#!/bin/sh\nset -eu\n" +
				`if [ "$1 $2" = "pr view" ]; then cat "$FORGE_FIXTURES/pr$3.json"; ` +
				`elif [ "$1 $2" = "pr list" ]; then author=""; while [ "$#" -gt 0 ]; do if [ "$1" = "--author" ]; then author="$2"; fi; shift; done; cat "$FORGE_FIXTURES/list-$author.json"; ` +
				"else echo \"forge-stub: unsupported: $*\" >&2; exit 1; fi\n"
			if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
				return err
			}
			env.Setenv("FORGE_CLI", stub)
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"upstream":              cmdUpstream,
			"upstream:pr":           cmdUpstreamPR,
			"upstream:move-pr":      cmdUpstreamMovePR,
			"upstream:commit":       cmdUpstreamCommit,
			"upstream:absorb-pr":    cmdUpstreamAbsorbPR,
			"forge:set":             cmdForgeSet,
			"forge:list":            cmdForgeList,
			"fork":                  cmdFork,
			"fork:author":           cmdForkAuthor,
			"mkpatch":               cmdMkPatch,
			"mkremotepatch":         cmdMkRemotePatch,
			"fork:remote":           cmdForkRemote,
			"forkrepo:branch":       cmdForkRepoBranch,
			"fork:branches":         cmdForkBranches,
			"check:pinned":          cmdCheckPinned,
			"lock:held-by-live-pid": cmdLockLive,
			"lock:held-by-dead-pid": cmdLockDead,
		},
	})
}

func sh(ts *testscript.TestScript, dir string, args ...string) string {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = ts.MkAbs(dir)
	cmd.Env = tsEnv(ts)
	out, err := cmd.CombinedOutput()
	if err != nil {
		ts.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func tsEnv(ts *testscript.TestScript) []string {
	var env []string
	for _, key := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "PATH", "HOME", "FORGE_FIXTURES"} {
		env = append(env, key+"="+ts.Getenv(key))
	}
	return env
}

func cmdUpstream(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 1 {
		ts.Fatalf("usage: upstream <dir>")
	}
	dir := args[0]
	sh(ts, ".", "git", "init", "-q", "-b", "main", ts.MkAbs(dir))
	sh(ts, dir, "git", "add", "-A")
	sh(ts, dir, "git", "commit", "-qm", "base")
}

func cmdUpstreamPR(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 4 {
		ts.Fatalf("usage: upstream:pr <dir> <n> <file> <content>")
	}
	dir, n, file, content := args[0], args[1], args[2], args[3]
	branch := "pr" + n
	sh(ts, dir, "git", "checkout", "-qb", branch, "main")
	writeFile(ts, filepath.Join(dir, file), content)
	sh(ts, dir, "git", "add", "-A")
	sh(ts, dir, "git", "commit", "-qm", "pr "+n)
	sh(ts, dir, "git", "update-ref", "refs/pull/"+n+"/head", branch)
	sh(ts, dir, "git", "checkout", "-q", "main")
	writeFixture(ts, n, "OPEN", sh(ts, dir, "git", "rev-parse", branch))
}

func cmdUpstreamMovePR(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 4 {
		ts.Fatalf("usage: upstream:move-pr <dir> <n> <file> <content>")
	}
	dir, n, file, content := args[0], args[1], args[2], args[3]
	branch := "pr" + n
	sh(ts, dir, "git", "checkout", "-q", branch)
	writeFile(ts, filepath.Join(dir, file), content)
	sh(ts, dir, "git", "add", "-A")
	sh(ts, dir, "git", "commit", "-qm", "pr "+n+" moved")
	sh(ts, dir, "git", "update-ref", "refs/pull/"+n+"/head", branch)
	sh(ts, dir, "git", "checkout", "-q", "main")
}

func cmdUpstreamCommit(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 3 {
		ts.Fatalf("usage: upstream:commit <dir> <file> <content>")
	}
	dir, file, content := args[0], args[1], args[2]
	writeFile(ts, filepath.Join(dir, file), content)
	sh(ts, dir, "git", "add", "-A")
	sh(ts, dir, "git", "commit", "-qm", "upstream: "+file)
}

func cmdUpstreamAbsorbPR(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: upstream:absorb-pr <dir> <n>")
	}
	dir, n := args[0], args[1]
	sh(ts, dir, "git", "merge", "-q", "--no-ff", "--no-edit", "pr"+n)
	writeFixture(ts, n, "MERGED", sh(ts, dir, "git", "rev-parse", "pr"+n))
}

func cmdForgeSet(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: forge:set <n> <STATE>")
	}
	info := readFixture(ts, args[0])
	writeFixture(ts, args[0], args[1], info.Head)
}

func cmdForgeList(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: forge:list <author> <n[,n...]>")
	}
	var items []struct {
		Number int    `json:"number"`
		Head   string `json:"headRefOid"`
	}
	if args[1] != "none" {
		for _, raw := range strings.Split(args[1], ",") {
			var n int
			if _, err := fmt.Sscanf(raw, "%d", &n); err != nil {
				ts.Fatalf("bad PR number %q", raw)
			}
			info := readFixture(ts, raw)
			items = append(items, struct {
				Number int    `json:"number"`
				Head   string `json:"headRefOid"`
			}{Number: n, Head: info.Head})
		}
	}
	data, _ := json.Marshal(items)
	writeFile(ts, filepath.Join(ts.Getenv("FORGE_FIXTURES"), "list-"+args[0]+".json"), string(data))
}

func cmdFork(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 3 {
		ts.Fatalf("usage: fork <dir> <upstream-dir> <track>")
	}
	dir, up, track := args[0], args[1], args[2]
	if track == "none" {
		track = ""
	}
	ts.Check(os.MkdirAll(ts.MkAbs(filepath.Join(dir, "patches")), 0o755))
	writeFile(ts, filepath.Join(dir, ".fork", "fork.toml"), fmt.Sprintf(
		"[upstream]\nrepo = \"test/up\"\nurl = \"file://%s\"\nbranch = \"main\"\nsha = \"\"\n\n"+
			"[prs]\ntrack = [%s]\n", ts.MkAbs(up), track))
}

func cmdForkAuthor(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 3 {
		ts.Fatalf("usage: fork:author <dir> <upstream-dir> <author>")
	}
	dir, up, author := args[0], args[1], args[2]
	ts.Check(os.MkdirAll(ts.MkAbs(filepath.Join(dir, "patches")), 0o755))
	writeFile(ts, filepath.Join(dir, ".fork", "fork.toml"), fmt.Sprintf(
		"[upstream]\nrepo = \"test/up\"\nurl = \"file://%s\"\nbranch = \"main\"\nsha = \"\"\n\n"+
			"[prs]\nauthor = \"%s\"\n", ts.MkAbs(up), author))
}

func cmdMkPatch(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 5 {
		ts.Fatalf("usage: mkpatch <upstream-dir> <fork-dir> <name> <file> <content>")
	}
	up, fork, name, file, content := args[0], args[1], args[2], args[3], args[4]
	scratch := ts.MkAbs(".scratch-" + name)
	sh(ts, ".", "git", "clone", "-q", "file://"+ts.MkAbs(up), scratch)
	ts.Check(os.WriteFile(filepath.Join(scratch, file), []byte(content), 0o644))
	sh(ts, scratch, "git", "add", "-A")
	sh(ts, scratch, "git", "commit", "-qm", "local: "+name)
	cmd := exec.Command("git", "format-patch", "-q", "-1", "--stdout")
	cmd.Dir = scratch
	cmd.Env = tsEnv(ts)
	patch, err := cmd.Output()
	ts.Check(err)
	writeFile(ts, filepath.Join(fork, "patches", name), string(patch))
	ts.Check(os.RemoveAll(scratch))
}

// cmdMkRemotePatch builds a format-patch file at an arbitrary path (not inside
// the fork's patches/ dir) so it can be served as a remote patch URL.
func cmdMkRemotePatch(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 4 {
		ts.Fatalf("usage: mkremotepatch <upstream-dir> <dest> <file> <content>")
	}
	up, dest, file, content := args[0], args[1], args[2], args[3]
	scratch := ts.MkAbs(".scratch-remote-" + file)
	sh(ts, ".", "git", "clone", "-q", "file://"+ts.MkAbs(up), scratch)
	ts.Check(os.WriteFile(filepath.Join(scratch, file), []byte(content), 0o644))
	sh(ts, scratch, "git", "add", "-A")
	sh(ts, scratch, "git", "commit", "-qm", "remote: "+file)
	cmd := exec.Command("git", "format-patch", "-q", "-1", "--stdout")
	cmd.Dir = scratch
	cmd.Env = tsEnv(ts)
	patch, err := cmd.Output()
	ts.Check(err)
	writeFile(ts, dest, string(patch))
	ts.Check(os.RemoveAll(scratch))
}

// cmdForkRemote appends a [[patches.remote]] entry to a fork's manifest,
// pinning the file at dest by its real sha256 (or a bogus one when "bad").
func cmdForkRemote(ts *testscript.TestScript, neg bool, args []string) {
	if neg || (len(args) != 2 && len(args) != 3) {
		ts.Fatalf("usage: fork:remote <fork-dir> <dest> [bad]")
	}
	dir, dest := args[0], args[1]
	data, err := os.ReadFile(ts.MkAbs(dest))
	ts.Check(err)
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	if len(args) == 3 && args[2] == "bad" {
		sha = strings.Repeat("0", 64)
	}
	tomlPath := ts.MkAbs(filepath.Join(dir, ".fork", "fork.toml"))
	existing, err := os.ReadFile(tomlPath)
	ts.Check(err)
	entry := fmt.Sprintf("\n[[patches.remote]]\nurl = \"file://%s\"\nsha256 = \"%s\"\n",
		ts.MkAbs(dest), sha)
	ts.Check(os.WriteFile(tomlPath, append(existing, []byte(entry)...), 0o644))
}

// cmdForkRepoBranch creates a branch in a repo (the fork remote holding
// internal work), branched from main with one commit adding <file>.
func cmdForkRepoBranch(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 4 {
		ts.Fatalf("usage: forkrepo:branch <repo-dir> <branch> <file> <content>")
	}
	dir, branch, file, content := args[0], args[1], args[2], args[3]
	sh(ts, dir, "git", "checkout", "-qb", branch, "main")
	writeFile(ts, filepath.Join(dir, file), content)
	sh(ts, dir, "git", "add", "-A")
	sh(ts, dir, "git", "commit", "-qm", "branch "+branch)
	sh(ts, dir, "git", "checkout", "-q", "main")
}

// cmdForkBranches adds a [branches] table to a fork's manifest, pointing at
// the fork remote and tracking the listed branches.
func cmdForkBranches(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 3 {
		ts.Fatalf("usage: fork:branches <fork-dir> <fork-remote-dir> <b1[,b2...]>")
	}
	dir, remote, list := args[0], args[1], args[2]
	quoted := make([]string, 0)
	for _, b := range strings.Split(list, ",") {
		quoted = append(quoted, fmt.Sprintf("%q", b))
	}
	tomlPath := ts.MkAbs(filepath.Join(dir, ".fork", "fork.toml"))
	existing, err := os.ReadFile(tomlPath)
	ts.Check(err)
	entry := fmt.Sprintf("\n[branches]\nurl = \"file://%s\"\ntrack = [%s]\n",
		ts.MkAbs(remote), strings.Join(quoted, ", "))
	ts.Check(os.WriteFile(tomlPath, append(existing, []byte(entry)...), 0o644))
}

func cmdCheckPinned(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 2 {
		ts.Fatalf("usage: check:pinned <fork-dir> <upstream-dir>")
	}
	head := sh(ts, args[1], "git", "rev-parse", "main")
	data, err := os.ReadFile(ts.MkAbs(filepath.Join(args[0], ".fork", "fork.toml")))
	ts.Check(err)
	pinned := strings.Contains(string(data), `sha = "`+head+`"`)
	if pinned == neg {
		ts.Fatalf("fork.toml pinned-to-upstream-head = %v, want %v\n%s", pinned, !neg, data)
	}
}

func cmdLockLive(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 1 {
		ts.Fatalf("usage: lock:held-by-live-pid <fork-dir>")
	}
	writeFile(ts, filepath.Join(args[0], ".fork", "assemble.lock"),
		fmt.Sprintf("%d", os.Getpid()))
}

func cmdLockDead(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 1 {
		ts.Fatalf("usage: lock:held-by-dead-pid <fork-dir>")
	}
	cmd := exec.Command("true")
	ts.Check(cmd.Start())
	pid := cmd.Process.Pid
	ts.Check(cmd.Wait())
	writeFile(ts, filepath.Join(args[0], ".fork", "assemble.lock"), fmt.Sprintf("%d", pid))
}

func writeFile(ts *testscript.TestScript, rel, content string) {
	abs := ts.MkAbs(rel)
	ts.Check(os.MkdirAll(filepath.Dir(abs), 0o755))
	ts.Check(os.WriteFile(abs, []byte(content), 0o644))
}

func writeFixture(ts *testscript.TestScript, n, state, head string) {
	data, _ := json.Marshal(prInfo{State: state, Head: head})
	writeFile(ts, filepath.Join(ts.Getenv("FORGE_FIXTURES"), "pr"+n+".json"), string(data))
}

func readFixture(ts *testscript.TestScript, n string) prInfo {
	data, err := os.ReadFile(filepath.Join(ts.Getenv("FORGE_FIXTURES"), "pr"+n+".json"))
	ts.Check(err)
	var info prInfo
	ts.Check(json.Unmarshal(data, &info))
	return info
}
