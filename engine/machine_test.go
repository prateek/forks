package main

import "testing"

func TestNextIntent(t *testing.T) {
	tests := []struct {
		name string
		in   RunState
		want Intent
	}{
		{
			name: "merge next PR",
			in:   RunState{Status: phaseMergingPRs, PRQueue: []int{2, 3}},
			want: Intent{Kind: intentMergePR, PR: 2},
		},
		{
			name: "transition to branches",
			in:   RunState{Status: phaseMergingPRs},
			want: Intent{Kind: intentTransitionToBranches},
		},
		{
			name: "continue merge",
			in:   RunState{Status: phaseAwaitingMergeResolution, PRQueue: []int{5}},
			want: Intent{Kind: intentContinueMerge, PR: 5},
		},
		{
			name: "merge next branch",
			in:   RunState{Status: phaseMergingBranches, BranchQueue: []string{"feat-a", "feat-b"}},
			want: Intent{Kind: intentMergeBranch, Branch: "feat-a"},
		},
		{
			name: "transition to patches after branches",
			in:   RunState{Status: phaseMergingBranches},
			want: Intent{Kind: intentTransitionToPatches},
		},
		{
			name: "continue branch",
			in:   RunState{Status: phaseAwaitingBranchResolution, BranchQueue: []string{"feat-a"}},
			want: Intent{Kind: intentContinueBranch, Branch: "feat-a"},
		},
		{
			name: "apply next patch",
			in:   RunState{Status: phaseApplyingPatches, PatchQueue: []string{"0001.patch"}},
			want: Intent{Kind: intentApplyPatch, Patch: "0001.patch"},
		},
		{
			name: "finalize after patches",
			in:   RunState{Status: phaseApplyingPatches},
			want: Intent{Kind: intentTransitionToFinalize},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextIntent(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("NextIntent() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestTerminalResult(t *testing.T) {
	tests := []struct {
		name string
		plan LogicalPlan
		want ResultKind
	}{
		{
			name: "retire",
			plan: LogicalPlan{},
			want: resultRetire,
		},
		{
			name: "no op",
			plan: LogicalPlan{
				PRHeads:     map[string]string{"1": "abc"},
				PatchDigest: map[string]string{"0001.patch": "def"},
			},
			want: resultNoOp,
		},
		{
			name: "needs sync",
			plan: LogicalPlan{
				PRHeads:     map[string]string{"1": "abc"},
				PatchDigest: map[string]string{"0001.patch": "def"},
				Drift:       []string{"upstream moved"},
			},
			want: resultNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.plan.TerminalResult()
			if tt.want == resultNone {
				if got != nil {
					t.Fatalf("TerminalResult() = %#v, want nil", got)
				}
				return
			}
			if got == nil || got.Kind != tt.want {
				t.Fatalf("TerminalResult() = %#v, want %s", got, tt.want)
			}
		})
	}
}
