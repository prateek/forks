package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const conflictPrompt = `You are resolving merge conflicts inside a downstream-fork assembly workspace.

Operation in flight: %s
Conflicted files:
%s

Policy:
- Preserve the intent of BOTH sides: upstream's change and the fork patch/PR.
- Prefer minimal, mechanical resolutions. Do not refactor or reformat.
- If the conflict requires a product decision (both sides change behavior in
  incompatible ways), abort by exiting nonzero WITHOUT staging anything.

Completion contract:
- Edit each conflicted file until no conflict markers remain.
- ` + "`git add`" + ` every resolved file.
- Do NOT commit and do NOT run ` + "`git merge --continue` / `git am --continue`" + `;
  exit 0 and the caller reruns ` + "`assemble --resume`" + ` to finish.
`

type Engine struct {
	cfg      *Config
	ws       string
	store    *FileStateStore
	git      *GitExecutor
	forge    ForgeClient
	log      *slog.Logger
	messages []string

	patchSources map[string]string

	pauseOperation string
	pauseFiles     []string
	doneResult     ResultKind
	doneSummary    string
}

func (e *Engine) remotePatchDir() string {
	return filepath.Join(e.ws, ".git", "fork-remote-patches")
}

// materializePatchSources resolves every patch's on-disk path: local patches
// map to files under patches/, and each remote patch is fetched and sha256-
// verified into a workspace-private staging dir. Idempotent, so it runs on
// both Start and Resume (a resume is a fresh process with an empty map).
func (e *Engine) materializePatchSources() error {
	e.patchSources = map[string]string{}
	names, err := e.cfg.patchFiles()
	if err != nil {
		return err
	}
	for _, name := range names {
		e.patchSources[name] = filepath.Join(e.cfg.patchesDir(), name)
	}
	for _, rp := range e.cfg.remotePatches {
		name := remotePatchName(rp.SHA256)
		dest := filepath.Join(e.remotePatchDir(), rp.SHA256+".patch")
		if err := FetchRemotePatch(rp.URL, rp.SHA256, dest); err != nil {
			return err
		}
		e.log.Info("remote patch fetched", slog.String("url", rp.URL), slog.String("sha256", rp.SHA256))
		e.patchSources[name] = dest
	}
	return nil
}

// orderedPatchNames lists local patch files (filename order) then remote
// patches (fork.toml order); this is the order they are applied.
func (e *Engine) orderedPatchNames() ([]string, error) {
	names, err := e.cfg.patchFiles()
	if err != nil {
		return nil, err
	}
	for _, rp := range e.cfg.remotePatches {
		names = append(names, remotePatchName(rp.SHA256))
	}
	return names, nil
}

func NewEngine(cfg *Config, ws string, store *FileStateStore, git *GitExecutor, forge ForgeClient, log *slog.Logger) *Engine {
	return &Engine{cfg: cfg, ws: ws, store: store, git: git, forge: forge, log: log}
}

func (e *Engine) say(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	e.messages = append(e.messages, msg)
	e.log.Info(msg)
}

func (e *Engine) result(kind ResultKind, summary string) *RunResult {
	return &RunResult{
		Kind:     kind,
		Summary:  summary,
		Messages: append([]string(nil), e.messages...),
	}
}

func (e *Engine) Start() (*RunResult, error) {
	if e.store.Exists() {
		return nil, fmt.Errorf("assembly already in flight (status=%s); use --resume or --abort", e.store.PeekStatus())
	}
	if err := e.git.PrepareWorkspace(e.cfg, e.ws); err != nil {
		return nil, err
	}
	if err := e.git.ScrubWorkspace(e.ws); err != nil {
		return nil, err
	}
	if err := e.materializePatchSources(); err != nil {
		return nil, err
	}
	plan, err := e.BuildLogicalPlan()
	if err != nil {
		return nil, err
	}
	if result := plan.TerminalResult(); result != nil {
		e.log.Info("decision", slog.String("result", string(result.Kind)))
		result.Messages = append([]string(nil), e.messages...)
		return result, nil
	}
	for _, reason := range plan.Drift {
		e.log.Info("drift", slog.String("reason", reason))
	}
	e.log.Info("decision",
		slog.String("result", "assemble"),
		slog.Int("prs", len(plan.PRHeads)),
		slog.Int("patches", len(plan.PatchDigest)),
		slog.String("sha", plan.UpstreamSHA))

	runPlan, err := e.BuildRunPlan(plan)
	if err != nil {
		return nil, err
	}
	e.messages = append(e.messages, runPlan.Messages...)
	if err := e.store.Save(runPlan.State); err != nil {
		return nil, err
	}
	return e.stepUntilDone(runPlan.State)
}

func (e *Engine) BuildLogicalPlan() (LogicalPlan, error) {
	newSHA, err := e.git.UpstreamHead(e.ws, e.cfg.upstreamBranch)
	if err != nil {
		return LogicalPlan{}, err
	}
	e.log.Info("upstream head",
		slog.String("branch", e.cfg.upstreamBranch),
		slog.String("sha", newSHA),
		slog.String("pinned_sha", orNoneShort(e.cfg.pinnedSHA)))
	prHeads, err := e.forge.ResolvePRSet(e.cfg)
	if err != nil {
		return LogicalPlan{}, err
	}
	branchHeads, err := e.resolveBranchHeads()
	if err != nil {
		return LogicalPlan{}, err
	}
	digest, err := patchesDigest(e.cfg)
	if err != nil {
		return LogicalPlan{}, err
	}
	e.log.Info("inputs",
		slog.Int("open_prs", len(prHeads)),
		slog.Int("branches", len(branchHeads)),
		slog.Int("patch_files", len(digest)))
	lock, err := loadLock(e.cfg)
	if err != nil {
		return LogicalPlan{}, err
	}
	head, err := e.git.CurrentHead(e.ws)
	if err != nil {
		return LogicalPlan{}, err
	}
	return LogicalPlan{
		UpstreamSHA: newSHA,
		PRHeads:     prHeads,
		BranchHeads: branchHeads,
		PatchDigest: digest,
		Drift:       explainDrift(lock, newSHA, prHeads, branchHeads, digest, head),
	}, nil
}

// resolveBranchHeads reads each tracked branch's tip from the fork remote
// without fetching, so drift can be judged before any assembly work.
func (e *Engine) resolveBranchHeads() (map[string]string, error) {
	if !e.cfg.hasBranches {
		return nil, nil
	}
	heads := map[string]string{}
	for _, br := range e.cfg.branches {
		head, err := e.git.BranchHead(e.ws, e.cfg.branchURL, br)
		if err != nil {
			return nil, err
		}
		heads[br] = head
		e.log.Info("branch tracked", slog.String("branch", br), slog.String("head", head))
	}
	return heads, nil
}

func (e *Engine) BuildRunPlan(plan LogicalPlan) (RunPlan, error) {
	if err := e.git.Checkout(e.ws, plan.UpstreamSHA); err != nil {
		return RunPlan{}, err
	}
	date, err := e.git.CommitDate(e.ws, plan.UpstreamSHA)
	if err != nil {
		return RunPlan{}, err
	}
	e.git.PinCommitMeta(date)
	if err := e.git.PreloadRerere(e.cfg, e.ws); err != nil {
		return RunPlan{}, err
	}
	var messages []string
	var branchQueue, droppedBranches []string
	for _, br := range e.cfg.branches {
		if _, err := e.git.FetchBranch(e.ws, e.cfg.branchURL, br); err != nil {
			return RunPlan{}, err
		}
		if e.git.BranchAlreadyUpstream(e.ws, br) {
			messages = append(messages, fmt.Sprintf("branch %s is already upstream; dropping", br))
			droppedBranches = append(droppedBranches, br)
		} else {
			branchQueue = append(branchQueue, br)
		}
	}
	var dropped, queue []string
	names, err := e.orderedPatchNames()
	if err != nil {
		return RunPlan{}, err
	}
	for _, name := range names {
		if e.git.PatchAlreadyPresent(e.ws, e.patchSources[name]) {
			messages = append(messages, fmt.Sprintf("%s is already upstream; dropping", name))
			dropped = append(dropped, name)
		} else {
			queue = append(queue, name)
		}
	}
	runPlan := NewRunPlan(plan, queue, dropped, branchQueue, droppedBranches)
	runPlan.Messages = messages
	return runPlan, nil
}

func (e *Engine) Resume() (*RunResult, error) {
	if !e.store.Exists() {
		return nil, fmt.Errorf("--resume: no assembly in progress (state file missing)")
	}
	s, err := e.store.Load()
	if err != nil {
		return nil, err
	}
	if !s.awaiting() {
		return nil, fmt.Errorf("--resume: state %q is not awaiting; run --abort", s.Status)
	}
	if err := e.materializePatchSources(); err != nil {
		return nil, err
	}
	reverted, err := e.git.EnforceResumeContract(e.ws, s.ConflictFiles)
	if err != nil {
		return nil, err
	}
	for _, path := range reverted {
		e.say("resume contract: reverted resolver change outside the conflicted set: %s", path)
	}
	if !e.git.IsAncestor(e.ws, s.SHA) {
		return nil, fmt.Errorf("--resume: workspace HEAD changed; run --abort")
	}
	date, err := e.git.CommitDate(e.ws, s.SHA)
	if err != nil {
		return nil, err
	}
	e.git.PinCommitMeta(date)
	e.log.Info("resume",
		slog.String("status", string(s.Status)),
		slog.String("sha", s.SHA),
		slog.Int("prs_left", len(s.PRQueue)),
		slog.Int("patches_left", len(s.PatchQueue)))
	return e.stepUntilDone(s)
}

func (e *Engine) Abort() (*RunResult, error) {
	if !e.store.Exists() {
		e.messages = append(e.messages, "nothing to abort")
		return e.result(resultNone, ""), nil
	}
	status := e.store.PeekStatus()
	abortedMerge := e.git.AbortMerge(e.ws)
	abortedAm := e.git.AbortPatch(e.ws)
	e.log.Info("abort",
		slog.String("status", status),
		slog.Bool("merge_aborted", abortedMerge.OK),
		slog.Bool("am_aborted", abortedAm.OK))
	if err := e.store.Clear(); err != nil {
		return nil, err
	}
	e.messages = append(e.messages, "in-flight assembly discarded; next run starts fresh")
	return e.result(resultNone, ""), nil
}

func (e *Engine) stepUntilDone(s *RunState) (*RunResult, error) {
	for {
		outcome, err := e.step(s)
		if err != nil {
			return nil, err
		}
		switch outcome {
		case outcomePause:
			return e.pauseResult()
		case outcomeDone:
			if e.doneResult != "" {
				e.log.Info("result", slog.String("result", string(e.doneResult)))
				return e.result(e.doneResult, e.doneSummary), nil
			}
			return e.result(resultNone, ""), nil
		}
	}
}

type stepOutcome int

const (
	outcomeContinue stepOutcome = iota
	outcomePause
	outcomeDone
)

func (e *Engine) step(s *RunState) (stepOutcome, error) {
	intent, err := NextIntent(*s)
	if err != nil {
		return outcomeDone, err
	}
	switch intent.Kind {
	case intentTransitionToBranches:
		e.log.Info("state transition", slog.String("from", string(phaseMergingPRs)), slog.String("to", string(phaseMergingBranches)))
		s.Status = phaseMergingBranches
		return outcomeContinue, e.store.Save(s)
	case intentTransitionToPatches:
		e.log.Info("state transition", slog.String("from", string(phaseMergingBranches)), slog.String("to", string(phaseApplyingPatches)))
		s.Status = phaseApplyingPatches
		return outcomeContinue, e.store.Save(s)
	case intentMergePR:
		return e.mergePR(s, intent.PR)
	case intentContinueMerge:
		return e.continueMerge(s, intent.PR)
	case intentMergeBranch:
		return e.mergeBranch(s, intent.Branch)
	case intentContinueBranch:
		return e.continueBranch(s, intent.Branch)
	case intentTransitionToFinalize:
		e.log.Info("state transition", slog.String("from", string(phaseApplyingPatches)), slog.String("to", string(phaseFinalizing)))
		s.Status = phaseFinalizing
		return outcomeContinue, e.store.Save(s)
	case intentApplyPatch:
		return e.applyPatch(s, intent.Patch)
	case intentContinuePatch:
		return e.continuePatch(s, intent.Patch)
	case intentFinalize:
		return e.finalize(s)
	default:
		return outcomeDone, fmt.Errorf("unknown intent %d", intent.Kind)
	}
}

func (e *Engine) mergePR(s *RunState, n int) (stepOutcome, error) {
	head, err := e.git.FetchPRHead(e.ws, n)
	if err != nil {
		return outcomeDone, err
	}
	if want := s.PRHeads[fmt.Sprintf("%d", n)]; want != "" && head != want {
		return outcomeDone, fmt.Errorf("PR #%d moved during sync: forge reported %.12s, fetch found %.12s; rerun assemble", n, want, head)
	}
	s.InFlightHead = head
	res := e.git.MergePR(e.ws, n, head)
	if !res.OK {
		outcome, err := e.finishConflictedOp(s, phaseAwaitingMergeResolution,
			fmt.Sprintf("merge of upstream PR #%d", n),
			e.git.CommitMerge, fmt.Sprintf("PR #%d", n), res)
		if outcome != outcomeContinue || err != nil {
			return outcome, err
		}
	} else {
		e.log.Info("PR merged", slog.Int("pr", n), slog.String("head", head))
	}
	s.InFlightHead = ""
	s.PRQueue = s.PRQueue[1:]
	return outcomeContinue, e.store.Save(s)
}

func (e *Engine) continueMerge(s *RunState, n int) (stepOutcome, error) {
	if !e.git.MergeInProgress(e.ws) {
		return outcomeDone, fmt.Errorf("--resume: PR #%d merge no longer in progress; run --abort", n)
	}
	mergeHead, err := e.git.MergeHead(e.ws)
	if err != nil {
		return outcomeDone, err
	}
	if mergeHead != s.InFlightHead {
		return outcomeDone, fmt.Errorf("--resume: merge head %.12s is not PR #%d; run --abort", mergeHead, n)
	}
	if files := e.git.ConflictedFiles(e.ws); len(files) > 0 {
		return outcomeDone, fmt.Errorf("--resume: PR #%d has unresolved conflicts; git add resolved files", n)
	}
	if err := e.git.CommitMerge(e.ws); err != nil {
		return outcomeDone, err
	}
	if err := e.git.ClearResolverContext(e.ws); err != nil {
		return outcomeDone, err
	}
	e.log.Info("externally-resolved merge committed", slog.Int("pr", n), slog.String("next_state", string(phaseMergingPRs)))
	s.InFlightHead = ""
	s.PRQueue = s.PRQueue[1:]
	s.Status = phaseMergingPRs
	s.ConflictFiles = nil
	return outcomeContinue, e.store.Save(s)
}

func (e *Engine) mergeBranch(s *RunState, name string) (stepOutcome, error) {
	head, err := e.git.FetchBranch(e.ws, e.cfg.branchURL, name)
	if err != nil {
		return outcomeDone, err
	}
	if want := s.BranchHeads[name]; want != "" && head != want {
		return outcomeDone, fmt.Errorf("branch %q moved during sync: planned %.12s, fetch found %.12s; rerun assemble", name, want, head)
	}
	s.InFlightHead = head
	res := e.git.MergeBranch(e.ws, name, head)
	if !res.OK {
		outcome, err := e.finishConflictedOp(s, phaseAwaitingBranchResolution,
			fmt.Sprintf("merge of branch %s", name),
			e.git.CommitMerge, fmt.Sprintf("branch %s", name), res)
		if outcome != outcomeContinue || err != nil {
			return outcome, err
		}
	} else {
		e.log.Info("branch merged", slog.String("branch", name), slog.String("head", head))
	}
	s.InFlightHead = ""
	s.BranchQueue = s.BranchQueue[1:]
	return outcomeContinue, e.store.Save(s)
}

func (e *Engine) continueBranch(s *RunState, name string) (stepOutcome, error) {
	if !e.git.MergeInProgress(e.ws) {
		return outcomeDone, fmt.Errorf("--resume: branch %s merge no longer in progress; run --abort", name)
	}
	mergeHead, err := e.git.MergeHead(e.ws)
	if err != nil {
		return outcomeDone, err
	}
	if mergeHead != s.InFlightHead {
		return outcomeDone, fmt.Errorf("--resume: merge head %.12s is not branch %s; run --abort", mergeHead, name)
	}
	if files := e.git.ConflictedFiles(e.ws); len(files) > 0 {
		return outcomeDone, fmt.Errorf("--resume: branch %s has unresolved conflicts; git add resolved files", name)
	}
	if err := e.git.CommitMerge(e.ws); err != nil {
		return outcomeDone, err
	}
	if err := e.git.ClearResolverContext(e.ws); err != nil {
		return outcomeDone, err
	}
	e.log.Info("externally-resolved branch merge committed", slog.String("branch", name), slog.String("next_state", string(phaseMergingBranches)))
	s.InFlightHead = ""
	s.BranchQueue = s.BranchQueue[1:]
	s.Status = phaseMergingBranches
	s.ConflictFiles = nil
	return outcomeContinue, e.store.Save(s)
}

func (e *Engine) applyPatch(s *RunState, name string) (stepOutcome, error) {
	patch := e.patchSources[name]
	if patch == "" {
		return outcomeDone, fmt.Errorf("patch %s is queued in the in-flight state but is no longer configured; restore it or --abort", name)
	}
	if _, err := os.Stat(patch); err != nil {
		return outcomeDone, fmt.Errorf("patch %s is queued in the in-flight state but its source is missing; restore it or --abort", name)
	}
	res := e.git.ApplyPatch(e.ws, patch)
	switch {
	case res.OK && strings.Contains(res.Explain, "Patch already applied"):
		e.say("%s: content already present in the assembled tree (an open PR carries it); keeping the file", name)
	case res.OK:
		e.log.Info("patch applied", slog.String("patch", name))
	case e.git.PatchAlreadyPresent(e.ws, patch):
		if e.git.PatchInProgress(e.ws) {
			if err := e.git.SkipPatch(e.ws); err != nil {
				return outcomeDone, err
			}
		}
		e.say("%s: content already present in the assembled tree (an open PR carries it); keeping the file", name)
	default:
		outcome, err := e.finishConflictedOp(s, phaseAwaitingPatchResolution,
			fmt.Sprintf("application of local patch %s", name),
			e.git.ContinuePatch, fmt.Sprintf("patch %s", name), res)
		if outcome != outcomeContinue || err != nil {
			return outcome, err
		}
	}
	s.PatchQueue = s.PatchQueue[1:]
	return outcomeContinue, e.store.Save(s)
}

func (e *Engine) continuePatch(s *RunState, name string) (stepOutcome, error) {
	if !e.git.PatchInProgress(e.ws) {
		return outcomeDone, fmt.Errorf("--resume: %s no longer in progress; run --abort", name)
	}
	if files := e.git.ConflictedFiles(e.ws); len(files) > 0 {
		return outcomeDone, fmt.Errorf("--resume: %s has unresolved conflicts; git add resolved files", name)
	}
	if err := e.git.ContinuePatch(e.ws); err != nil {
		return outcomeDone, err
	}
	if err := e.git.ClearResolverContext(e.ws); err != nil {
		return outcomeDone, err
	}
	e.log.Info("externally-resolved patch committed", slog.String("patch", name), slog.String("next_state", string(phaseApplyingPatches)))
	s.PatchQueue = s.PatchQueue[1:]
	s.Status = phaseApplyingPatches
	s.ConflictFiles = nil
	return outcomeContinue, e.store.Save(s)
}

func (e *Engine) finalize(s *RunState) (stepOutcome, error) {
	openPRs := prNumbers(s.PRHeads)
	dropped := map[string]bool{}
	for _, name := range s.DroppedPatches {
		dropped[name] = true
	}
	kept := map[string]string{}
	for name, sum := range s.Digest {
		if !dropped[name] {
			kept[name] = sum
		}
	}
	droppedBranch := map[string]bool{}
	for _, name := range s.DroppedBranches {
		droppedBranch[name] = true
	}
	keptBranches := map[string]string{}
	var keptBranchNames []string
	for name, head := range s.BranchHeads {
		if !droppedBranch[name] {
			keptBranches[name] = head
		}
	}
	// keptBranchNames preserves fork.toml order for a stable pruned track list.
	for _, name := range e.cfg.branches {
		if !droppedBranch[name] {
			keptBranchNames = append(keptBranchNames, name)
		}
	}

	if err := e.git.HarvestRerere(e.cfg, e.ws); err != nil {
		return outcomeDone, err
	}
	assembledHead, err := e.git.CurrentHead(e.ws)
	if err != nil {
		return outcomeDone, err
	}
	update, err := bumpManifest(e.cfg, s.SHA, openPRs, keptBranchNames, s.DroppedPatches, s.DroppedBranches)
	if err != nil {
		return outcomeDone, err
	}
	for _, warning := range update.warnings {
		e.log.Warn("manifest warning", slog.String("warning", warning))
	}
	for _, note := range update.notes {
		e.log.Info("manifest note", slog.String("note", note))
	}
	e.log.Info("manifest updated", slog.String("upstream_sha", s.SHA))
	if err := writeLock(e.cfg, s.SHA, assembledHead, s.PRHeads, keptBranches, kept); err != nil {
		return outcomeDone, err
	}
	if err := e.store.Clear(); err != nil {
		return outcomeDone, err
	}

	if len(openPRs) == 0 && len(keptBranches) == 0 && len(kept) == 0 {
		e.doneResult, e.doneSummary = resultRetire, retireSummary
		return outcomeDone, nil
	}
	summary := []string{
		fmt.Sprintf("upstream %s@%.12s", e.cfg.upstreamRepo, s.SHA),
		"PRs merged: " + orNone(joinPRRefs(openPRs)),
	}
	if e.cfg.hasBranches || len(keptBranches) > 0 {
		summary = append(summary, "branches merged: "+orNone(strings.Join(keptBranchNames, ", ")))
	}
	summary = append(summary, fmt.Sprintf("patches applied: %d", len(kept)))
	summary = append(summary, update.notes...)
	e.doneResult, e.doneSummary = resultSynced, strings.Join(summary, "\n")
	return outcomeDone, nil
}

func (e *Engine) finishConflictedOp(s *RunState, awaitingStatus Phase, operation string,
	finish func(string) error, label string, failure OperationResult) (stepOutcome, error) {
	inFlight := e.git.MergeInProgress(e.ws) || e.git.PatchInProgress(e.ws)
	conflicts := e.git.ConflictedFiles(e.ws)
	if !inFlight || len(conflicts) == 0 {
		e.log.Error("operation failed without resolvable conflicts",
			slog.String("operation", operation),
			slog.String("output", failure.Explain))
		return outcomeDone, fmt.Errorf("%s failed without resolvable conflicts: %s", operation, failure.Explain)
	}
	resolved, err := e.git.RerereResolvedEverything(e.ws)
	if err != nil {
		return outcomeDone, err
	}
	if resolved {
		if err := e.git.AddResolved(e.ws, conflicts); err != nil {
			return outcomeDone, err
		}
		if err := finish(e.ws); err != nil {
			return outcomeDone, err
		}
		e.say("%s conflict auto-resolved from rerere cache", label)
		return outcomeContinue, nil
	}
	e.log.Info("pausing for resolver", slog.String("label", label))
	if err := e.git.SnapshotResolverContext(e.ws); err != nil {
		return outcomeDone, err
	}
	s.Status = awaitingStatus
	s.ConflictFiles = conflicts
	if err := e.store.Save(s); err != nil {
		return outcomeDone, err
	}
	e.pauseOperation, e.pauseFiles = operation, conflicts
	return outcomePause, nil
}

func (e *Engine) pauseResult() (*RunResult, error) {
	e.log.Warn("paused for conflicts",
		slog.String("operation", e.pauseOperation),
		slog.Any("files", e.pauseFiles))
	var fileList strings.Builder
	for _, f := range e.pauseFiles {
		fmt.Fprintf(&fileList, "  - %s\n", f)
	}
	promptPath := filepath.Join(e.ws, ".git", "fork-conflict-prompt.md")
	prompt := fmt.Sprintf(conflictPrompt, e.pauseOperation, strings.TrimRight(fileList.String(), "\n"))
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return nil, err
	}
	e.say("conflict prompt written to %s", promptPath)
	return e.result(resultConflict, fmt.Sprintf("%s: conflicts in %s",
		e.pauseOperation, strings.Join(e.pauseFiles, ", "))), nil
}
