package main

import (
	"fmt"
	"sort"
	"strings"
)

const retireSummary = "all tracked PRs merged/closed, no tracked branches, and no patches remain"

type Phase string

const (
	phaseMergingPRs               Phase = "merging_prs"
	phaseAwaitingMergeResolution  Phase = "awaiting_merge_resolution"
	phaseMergingBranches          Phase = "merging_branches"
	phaseAwaitingBranchResolution Phase = "awaiting_branch_resolution"
	phaseApplyingPatches          Phase = "applying_patches"
	phaseAwaitingPatchResolution  Phase = "awaiting_patch_resolution"
	phaseFinalizing               Phase = "finalizing"
)

type RunState struct {
	Version        int               `json:"version"`
	Status         Phase             `json:"status"`
	SHA            string            `json:"sha"`
	PRHeads         map[string]string `json:"pr_heads"`
	PRQueue         []int             `json:"pr_queue"`
	BranchHeads     map[string]string `json:"branch_heads,omitempty"`
	BranchQueue     []string          `json:"branch_queue,omitempty"`
	DroppedBranches []string          `json:"dropped_branches,omitempty"`
	PatchQueue      []string          `json:"patch_queue"`
	Digest          map[string]string `json:"digest"`
	DroppedPatches  []string          `json:"dropped_patches"`
	InFlightHead    string            `json:"in_flight_head,omitempty"`
	ConflictFiles   []string          `json:"conflict_files,omitempty"`
}

func (s *RunState) awaiting() bool {
	return s.Status == phaseAwaitingMergeResolution ||
		s.Status == phaseAwaitingBranchResolution ||
		s.Status == phaseAwaitingPatchResolution
}

type ResultKind string

const (
	resultNone     ResultKind = ""
	resultNoOp     ResultKind = "no_op"
	resultSynced   ResultKind = "synced"
	resultConflict ResultKind = "conflict"
	resultRetire   ResultKind = "retire"
)

type RunResult struct {
	Kind     ResultKind
	Summary  string
	Messages []string
}

type LogicalPlan struct {
	UpstreamSHA string
	PRHeads     map[string]string
	BranchHeads map[string]string
	PatchDigest map[string]string
	Drift       []string
}

func (p LogicalPlan) TerminalResult() *RunResult {
	if len(p.PRHeads) == 0 && len(p.BranchHeads) == 0 && len(p.PatchDigest) == 0 {
		return &RunResult{Kind: resultRetire, Summary: retireSummary}
	}
	if len(p.Drift) == 0 {
		return &RunResult{Kind: resultNoOp, Summary: "upstream, PR heads, branches, and patches unchanged since last sync"}
	}
	return nil
}

type RunPlan struct {
	State    *RunState
	Messages []string
}

func NewRunPlan(plan LogicalPlan, queue, dropped, branchQueue, droppedBranches []string) RunPlan {
	return RunPlan{
		State: &RunState{
			Status:          phaseMergingPRs,
			SHA:             plan.UpstreamSHA,
			PRHeads:         plan.PRHeads,
			PRQueue:         prNumbers(plan.PRHeads),
			BranchHeads:     plan.BranchHeads,
			BranchQueue:     branchQueue,
			DroppedBranches: droppedBranches,
			PatchQueue:      queue,
			Digest:          plan.PatchDigest,
			DroppedPatches:  dropped,
		},
	}
}

type OperationResult struct {
	OK      bool
	Explain string
}

type IntentKind int

const (
	intentTransitionToBranches IntentKind = iota
	intentTransitionToPatches
	intentMergePR
	intentContinueMerge
	intentMergeBranch
	intentContinueBranch
	intentTransitionToFinalize
	intentApplyPatch
	intentContinuePatch
	intentFinalize
)

type Intent struct {
	Kind   IntentKind
	PR     int
	Branch string
	Patch  string
}

func NextIntent(s RunState) (Intent, error) {
	switch s.Status {
	case phaseMergingPRs:
		if len(s.PRQueue) == 0 {
			return Intent{Kind: intentTransitionToBranches}, nil
		}
		return Intent{Kind: intentMergePR, PR: s.PRQueue[0]}, nil
	case phaseAwaitingMergeResolution:
		if len(s.PRQueue) == 0 {
			return Intent{}, fmt.Errorf("awaiting merge resolution with empty PR queue")
		}
		return Intent{Kind: intentContinueMerge, PR: s.PRQueue[0]}, nil
	case phaseMergingBranches:
		if len(s.BranchQueue) == 0 {
			return Intent{Kind: intentTransitionToPatches}, nil
		}
		return Intent{Kind: intentMergeBranch, Branch: s.BranchQueue[0]}, nil
	case phaseAwaitingBranchResolution:
		if len(s.BranchQueue) == 0 {
			return Intent{}, fmt.Errorf("awaiting branch resolution with empty branch queue")
		}
		return Intent{Kind: intentContinueBranch, Branch: s.BranchQueue[0]}, nil
	case phaseApplyingPatches:
		if len(s.PatchQueue) == 0 {
			return Intent{Kind: intentTransitionToFinalize}, nil
		}
		return Intent{Kind: intentApplyPatch, Patch: s.PatchQueue[0]}, nil
	case phaseAwaitingPatchResolution:
		if len(s.PatchQueue) == 0 {
			return Intent{}, fmt.Errorf("awaiting patch resolution with empty patch queue")
		}
		return Intent{Kind: intentContinuePatch, Patch: s.PatchQueue[0]}, nil
	case phaseFinalizing:
		return Intent{Kind: intentFinalize}, nil
	default:
		return Intent{}, fmt.Errorf("unknown state %q; discard it with --abort", s.Status)
	}
}

func prNumbers(prHeads map[string]string) []int {
	var numbers []int
	for k := range prHeads {
		if n, ok := atoiSafe(k); ok {
			numbers = append(numbers, n)
		}
	}
	sort.Ints(numbers)
	return numbers
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func orNoneShort(sha string) string {
	if sha == "" {
		return "none"
	}
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func joinPRRefs(numbers []int) string {
	parts := make([]string, len(numbers))
	for i, n := range numbers {
		parts[i] = fmt.Sprintf("#%d", n)
	}
	return strings.Join(parts, ", ")
}
