package seed

import (
	"testing"

	"github.com/hertz/captain/internal/flow"
)

// The seeded 寻道大千 flow must satisfy the v1 flow engine schema, otherwise
// the demo event is unusable on first boot (REQUIREMENTS §11.5).
func TestXundaoFlowIsValid(t *testing.T) {
	f, err := flow.Parse([]byte(xundaoFlow))
	if err != nil {
		t.Fatalf("seed flow invalid: %v", err)
	}
	want := []string{"checkin", "form", "game", "charity", "reward"}
	if len(f.Steps) != len(want) {
		t.Fatalf("steps = %d, want %d", len(f.Steps), len(want))
	}
	for i, s := range f.Steps {
		if s.Type != want[i] {
			t.Errorf("step %d type=%q want %q", i, s.Type, want[i])
		}
	}
}
