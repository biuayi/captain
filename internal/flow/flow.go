// Package flow models the linear v1 flow schema (ARCHITECTURE §4).
// Six step types, no branching: checkin form game charity reward result.
package flow

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	StepCheckin = "checkin"
	StepForm    = "form"
	StepGame    = "game"
	StepCharity = "charity"
	StepReward  = "reward"
	StepResult  = "result"
)

var validTypes = map[string]bool{
	StepCheckin: true, StepForm: true, StepGame: true,
	StepCharity: true, StepReward: true, StepResult: true,
}

type Step struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Title      string         `json:"title"`
	Required   bool           `json:"required"`
	Skippable  bool           `json:"skippable"`
	NextStepID *string        `json:"nextStepId"`
	Config     map[string]any `json:"config"`
}

type Flow struct {
	Version     int    `json:"version"`
	FlowID      string `json:"flowId"`
	Name        string `json:"name"`
	EntryStepID string `json:"entryStepId"`
	Steps       []Step `json:"steps"`
}

func Parse(raw []byte) (*Flow, error) {
	var f Flow
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("flow: invalid json: %w", err)
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

func (f *Flow) Validate() error {
	if len(f.Steps) == 0 {
		return errors.New("flow: no steps")
	}
	ids := make(map[string]bool, len(f.Steps))
	for _, s := range f.Steps {
		if s.ID == "" {
			return errors.New("flow: step missing id")
		}
		if ids[s.ID] {
			return fmt.Errorf("flow: duplicate step id %q", s.ID)
		}
		ids[s.ID] = true
		if !validTypes[s.Type] {
			return fmt.Errorf("flow: unknown step type %q", s.Type)
		}
	}
	if !ids[f.EntryStepID] {
		return fmt.Errorf("flow: entryStepId %q not found", f.EntryStepID)
	}
	for _, s := range f.Steps {
		if s.NextStepID != nil && !ids[*s.NextStepID] {
			return fmt.Errorf("flow: step %q nextStepId %q not found", s.ID, *s.NextStepID)
		}
	}
	return nil
}

func (f *Flow) Step(id string) (Step, bool) {
	for _, s := range f.Steps {
		if s.ID == id {
			return s, true
		}
	}
	return Step{}, false
}
