package issueworkflow

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func rulesDefinition() Definition {
	return Definition{Name: "Delivery", Steps: []Step{
		{StatusKey: "todo", Handler: Handler{Type: HandlerNone}},
		{StatusKey: "in_progress", Handler: Handler{Type: HandlerAgent, ID: "dev"}, Strict: true},
		{StatusKey: "in_review", Handler: Handler{Type: HandlerAgent, ID: "review"}, BackStatusKey: "in_progress", Strict: true, MaxReturns: 2},
		{StatusKey: "blocked", Handler: Handler{Type: HandlerNone}},
		{StatusKey: "done", Handler: Handler{Type: HandlerNone}},
		{StatusKey: "cancelled", Handler: Handler{Type: HandlerNone}},
	}}
}

func TestStrictStepAcceptsOnlyItsDeclaredMoves(t *testing.T) {
	def := rulesDefinition()

	if got, want := def.AllowedMoves("in_review"), []string{"done", "in_progress", "blocked"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("moves out of in_review = %v, want %v", got, want)
	}
	for _, to := range []string{"in_review", "blocked", "in_progress"} {
		if err := CheckMove(&def, "MUL-1", "in_progress", to); err != nil && to != "in_progress" {
			t.Fatalf("in_progress -> %s refused: %v", to, err)
		}
	}
	// Skipping the review is the move the rule exists for.
	err := CheckMove(&def, "MUL-1", "in_progress", "done")
	var refused *MoveNotAllowedError
	if !errors.As(err, &refused) {
		t.Fatalf("in_progress -> done = %v, want a refusal", err)
	}
	if !reflect.DeepEqual(refused.Allowed, []string{"in_review", "blocked"}) {
		t.Fatalf("refusal lists %v", refused.Allowed)
	}
	if msg := err.Error(); !strings.Contains(msg, "MUL-1") || !strings.Contains(msg, "in_review, blocked") {
		t.Fatalf("refusal does not tell the run what it may do: %q", msg)
	}
}

func TestStepWithoutTheRuleRefusesNothing(t *testing.T) {
	def := rulesDefinition()
	if err := CheckMove(&def, "MUL-1", "todo", "done"); err != nil {
		t.Fatalf("a step that is not strict refused a move: %v", err)
	}
	if err := CheckMove(&def, "MUL-1", "somewhere-else", "done"); err != nil {
		t.Fatalf("a status the workflow does not list refused a move: %v", err)
	}
	if err := CheckMove(nil, "MUL-1", "in_progress", "done"); err != nil {
		t.Fatalf("no workflow refused a move: %v", err)
	}
}

func TestReturnLimitCountsOnlyTheStepsOwnWayBack(t *testing.T) {
	def := rulesDefinition()
	if limit, ok := ReturnLimit(&def, "in_review", "in_progress"); !ok || limit != 2 {
		t.Fatalf("in_review -> in_progress = (%d, %v), want (2, true)", limit, ok)
	}
	for _, move := range [][2]string{{"in_review", "done"}, {"in_review", "blocked"}, {"in_progress", "todo"}} {
		if _, ok := ReturnLimit(&def, move[0], move[1]); ok {
			t.Fatalf("%s -> %s is not a return this workflow counts", move[0], move[1])
		}
	}
}

func TestRulesSurviveStorageAndAreValidated(t *testing.T) {
	def := rulesDefinition()
	raw, err := EncodeSteps(def.Steps)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	steps, err := DecodeSteps(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !steps[1].Strict || steps[2].MaxReturns != 2 || steps[0].Strict || steps[0].MaxReturns != 0 {
		t.Fatalf("rules after a round trip: %+v", steps)
	}

	// A cap with no way back counts nothing, so it is dropped.
	if got := NormalizeHandoff(Step{StatusKey: "in_progress", MaxReturns: 3}); got.MaxReturns != 0 {
		t.Fatalf("cap without a back status kept: %d", got.MaxReturns)
	}
	review := func(strict bool, maxReturns int) Step {
		return NormalizeHandoff(Step{StatusKey: "in_review", BackStatusKey: "in_progress", Strict: strict, MaxReturns: maxReturns})
	}
	if err := ValidateHandoff(review(false, 2), def); err != nil {
		t.Fatalf("a cap within the limit was refused: %v", err)
	}
	if err := ValidateHandoff(review(false, MaxReturnsLimit+1), def); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("a cap above the limit: %v", err)
	}
	// A run at the cap needs blocked to stop at.
	noBlocked := Definition{Steps: []Step{{StatusKey: "todo"}, {StatusKey: "in_progress"}, {StatusKey: "in_review"}, {StatusKey: "done"}, {StatusKey: "cancelled"}}}
	if err := ValidateHandoff(review(false, 2), noBlocked); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("a cap on a workflow without blocked: %v", err)
	}
	if err := ValidateHandoff(review(true, 0), noBlocked); err != nil {
		t.Fatalf("a strict step needs no blocked status: %v", err)
	}
}

func TestBriefTellsTheRunWhichRulesAreEnforced(t *testing.T) {
	def := rulesDefinition()
	brief := Brief(BriefInput{Workflow: def, StatusKey: "in_review", IssueIdentifier: "MUL-1"})
	for _, want := range []string{"only status moves this step accepts", "at most 2 time(s)"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief is missing %q:\n%s", want, brief)
		}
	}
	if plain := Brief(BriefInput{Workflow: def, StatusKey: "todo", IssueIdentifier: "MUL-1"}); strings.Contains(plain, "refused") {
		t.Fatalf("a step without rules mentions them:\n%s", plain)
	}
}
