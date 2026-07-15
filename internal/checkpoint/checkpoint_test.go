package checkpoint

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	got, err := Render(Checkpoint{
		Objective:      "Synthetic objective",
		Completed:      []string{"One"},
		Validation:     []string{"Pass"},
		NextAction:     "Stop",
		RemainingGates: []string{"Review"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "Checkpoint\n\nObjective:\nSynthetic objective\n" +
		"\nCompleted:\n- One\n" +
		"\nValidation:\n- Pass\n" +
		"\nNext action:\nStop\n" +
		"\nRemaining gates:\n- Review"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRenderRejectsContractViolations(t *testing.T) {
	valid := Checkpoint{Objective: "Synthetic", NextAction: "Stop"}
	tests := []struct {
		name   string
		mutate func(*Checkpoint)
	}{
		{name: "long objective", mutate: func(c *Checkpoint) { c.Objective = strings.Repeat("x", 1001) }},
		{name: "long next action", mutate: func(c *Checkpoint) { c.NextAction = strings.Repeat("x", 501) }},
		{name: "too many blockers", mutate: func(c *Checkpoint) {
			c.Blockers = make([]string, 21)
			for i := range c.Blockers {
				c.Blockers[i] = "x"
			}
		}},
		{name: "empty item", mutate: func(c *Checkpoint) { c.Completed = []string{" "} }},
		{name: "long item", mutate: func(c *Checkpoint) { c.Completed = []string{strings.Repeat("x", 501)} }},
		{name: "invalid utf-8", mutate: func(c *Checkpoint) { c.Objective = string([]byte{0xff}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			test.mutate(&value)
			if _, err := Render(value); err == nil {
				t.Fatal("invalid checkpoint accepted")
			}
		})
	}
}
