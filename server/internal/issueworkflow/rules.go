package issueworkflow

import (
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/issuestatus"
)

// Step rules. A step's next and back statuses are advice a run can ignore: it
// can mark the issue done from anywhere, or send it back for ever. Two
// optional rules make them binding on the run that works the issue.
//
//   - Strict: the run may only move the issue to the step's next status, its
//     back status, or blocked.
//   - MaxReturns: the run may send the issue to the back status at most that
//     many times; after that the move is refused and the run has to stop
//     with blocked, which is what tells whoever runs the parent.
//
// People are never bound, and neither is an agent acting on an issue it is
// not running: someone has to be able to overrule a rule.

// MaxReturnsLimit bounds the setting, not the work.
const MaxReturnsLimit = 20

// MoveNotAllowedError refuses a run's move out of a strict step. Its message
// lists the moves the step accepts because agents read it verbatim.
type MoveNotAllowedError struct {
	IssueIdentifier string
	From, To        string
	Allowed         []string
}

func (e *MoveNotAllowedError) Error() string {
	if len(e.Allowed) == 0 {
		return fmt.Sprintf("%s cannot be moved from %q to %q: this step's workflow accepts no status change from your run. Leave the status and comment instead.",
			e.IssueIdentifier, e.From, e.To)
	}
	return fmt.Sprintf("%s cannot be moved from %q to %q: from this step your run may only move it to one of: %s.",
		e.IssueIdentifier, e.From, e.To, strings.Join(e.Allowed, ", "))
}

// ReturnLimitError refuses sending an issue back once more than the step
// allows.
type ReturnLimitError struct {
	IssueIdentifier string
	From, To        string
	Limit           int
}

func (e *ReturnLimitError) Error() string {
	return fmt.Sprintf("%s has already been sent back from %q to %q %d time(s), the limit for this step. "+
		"Do not send it back again: set it to blocked and comment what is still wrong, so whoever runs the parent issue decides.",
		e.IssueIdentifier, e.From, e.To, e.Limit)
}

// AllowedMoves lists the statuses a run may move an issue to from a strict
// step, in the order the brief presents them. effective resolves a status key
// to its canonical behavior, as for the brief, so both name the same moves;
// nil leaves every key as itself.
func (d Definition) AllowedMoves(key string, effective func(string) string) []string {
	step, ok := d.Step(key)
	if !ok {
		return nil
	}
	var allowed []string
	add := func(k string) {
		if k == "" || k == key {
			return
		}
		for _, have := range allowed {
			if have == k {
				return
			}
		}
		allowed = append(allowed, k)
	}
	// A step without a handler is worked by whoever carries the issue across
	// it, and the brief sends them to the far end of that stretch.
	if span := d.handlerlessSpan(key, effective); span.ok {
		add(span.start)
		add(span.deliver)
	} else {
		add(d.nextStatus(key, effective))
	}
	add(step.BackStatusKey)
	if d.Has(issuestatus.Blocked) {
		add(issuestatus.Blocked)
	}
	return allowed
}

// CheckMove applies a strict step to a run's status change.
func CheckMove(def *Definition, identifier, from, to string, effective func(string) string) error {
	if def == nil || from == to {
		return nil
	}
	step, ok := def.Step(from)
	if !ok || !step.Strict {
		return nil
	}
	allowed := def.AllowedMoves(from, effective)
	for _, k := range allowed {
		if k == to {
			return nil
		}
	}
	return &MoveNotAllowedError{IssueIdentifier: identifier, From: from, To: to, Allowed: allowed}
}

// ReturnLimit reports the step's cap when the move is a return it counts.
func ReturnLimit(def *Definition, from, to string) (int, bool) {
	if def == nil {
		return 0, false
	}
	step, ok := def.Step(from)
	if !ok || step.MaxReturns <= 0 || step.BackStatusKey == "" || step.BackStatusKey != to {
		return 0, false
	}
	return step.MaxReturns, true
}

// normalizeRules drops a cap that has nothing to count.
func normalizeRules(s Step) Step {
	if s.MaxReturns < 0 || s.BackStatusKey == "" {
		s.MaxReturns = 0
	}
	return s
}

// validateRules checks a step's rules against its workflow.
func validateRules(s Step, def Definition) error {
	if s.MaxReturns > MaxReturnsLimit {
		return invalid("an issue can be sent back at most %d times", MaxReturnsLimit)
	}
	// A run at the cap needs somewhere to stop, and blocked is the status
	// that gets someone else's attention.
	if s.MaxReturns > 0 && !def.Has(issuestatus.Blocked) {
		return invalid("a limit on sending issues back needs the workflow to include %q", issuestatus.Blocked)
	}
	return nil
}

// writeStatusMapRules tells the run which of its step's rules are enforced,
// in the terms of the status map: the map lets the run pick any status, so a
// strict step names the ones it accepts.
func writeStatusMapRules(b *strings.Builder, in BriefInput, step Step) {
	if !step.Strict && (step.MaxReturns <= 0 || step.BackStatusKey == "") {
		return
	}
	b.WriteString("\n")
	if step.Strict {
		allowed := in.Workflow.AllowedMoves(in.StatusKey, in.EffectiveStatus)
		quoted := make([]string, len(allowed))
		for i, k := range allowed {
			quoted[i] = "`" + k + "`"
		}
		fmt.Fprintf(b, "The only status moves this step accepts from your run are to %s; any other is refused.\n", strings.Join(quoted, ", "))
	}
	if step.MaxReturns > 0 && step.BackStatusKey != "" {
		fmt.Fprintf(b, "Sending the issue back to `%s` is accepted at most %d time(s) per issue. Once that is used up, set `%s` and say what is still wrong.\n", step.BackStatusKey, step.MaxReturns, issuestatus.Blocked)
	}
}

// writeRules tells the run which of its step's rules are enforced.
func writeRules(b *strings.Builder, step Step) {
	if step.Strict {
		b.WriteString("These are the only status moves this step accepts from your run; any other is refused.\n")
	}
	if step.MaxReturns > 0 && step.BackStatusKey != "" {
		fmt.Fprintf(b, "Needs changes is accepted at most %d time(s) per issue. Once that is used up, use Can't proceed and say what is still wrong.\n", step.MaxReturns)
	}
}
