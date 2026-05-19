// Package flow models the linear v2 flow schema (DESIGN §SS-3). User-facing
// canonical stages R1-R4 (each optional) plus auxiliary info steps; no
// branching. Sequential gating chains over the *enabled* stages only.
package flow

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	StepCheckin = "checkin" // R1
	StepForm    = "form"    // R2 (survey)
	StepExam    = "exam"    // R3
	StepLottery = "lottery" // R4
	StepGame    = "game"    // auxiliary (legacy)
	StepCharity = "charity" // auxiliary
	StepReward  = "reward"  // auxiliary
	StepResult  = "result"  // auxiliary
)

var validTypes = map[string]bool{
	StepCheckin: true, StepForm: true, StepExam: true, StepLottery: true,
	StepGame: true, StepCharity: true, StepReward: true, StepResult: true,
}

// Stages in canonical order. A flow enables a subset; gating chains over
// whichever are present.
var StageOrder = []string{"R1", "R2", "R3", "R4"}

// stageType binds each stage to its required step type (DESIGN §SS-3).
var stageType = map[string]string{
	"R1": StepCheckin, "R2": StepForm, "R3": StepExam, "R4": StepLottery,
}

type Step struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Stage      string         `json:"stage"` // "", R1, R2, R3, R4
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
	stageSeen := map[string]bool{}
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
		if s.Stage != "" {
			want, ok := stageType[s.Stage]
			if !ok {
				return fmt.Errorf("flow: step %q unknown stage %q", s.ID, s.Stage)
			}
			if s.Type != want {
				return fmt.Errorf("flow: stage %s must be type %s, got %s", s.Stage, want, s.Type)
			}
			stageSeen[s.Stage] = true
		}
		if err := validateStepConfig(s); err != nil {
			return err
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

// validateStepConfig checks the minimal per-type config shape (SS3-04).
func validateStepConfig(s Step) error {
	switch s.Type {
	case StepCheckin:
		// days >= 0 (0 = R1 disabled / skipped)
		if v, ok := s.Config["days"]; ok {
			if d, ok := v.(float64); !ok || d < 0 {
				return fmt.Errorf("flow: step %q checkin.days must be >=0", s.ID)
			}
		}
	case StepForm:
		if _, ok := s.Config["fields"].([]any); !ok {
			return fmt.Errorf("flow: step %q form.config.fields[] required", s.ID)
		}
	case StepExam:
		mode, _ := s.Config["mode"].(string)
		if mode != "all" && mode != "random" {
			return fmt.Errorf("flow: step %q exam.mode must be all|random", s.ID)
		}
		if mode == "random" {
			if rc, ok := s.Config["randomCount"].(float64); !ok || rc <= 0 {
				return fmt.Errorf("flow: step %q exam.randomCount>0 required for random mode", s.ID)
			}
		}
	case StepLottery:
		// pools/prizes live in their own tables; config is light flags.
		if _, ok := s.Config["drawLimit"]; ok {
			if dl, ok := s.Config["drawLimit"].(float64); !ok || dl != 1 {
				return fmt.Errorf("flow: step %q lottery.drawLimit must be 1", s.ID)
			}
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

// EnabledStages returns the canonical-ordered stages actually present, and
// for R1 only counts it enabled when checkin.days > 0 (days==0 disables R1).
func (f *Flow) EnabledStages() []string {
	present := map[string]bool{}
	for _, s := range f.Steps {
		if s.Stage == "" {
			continue
		}
		if s.Stage == "R1" {
			if d, ok := s.Config["days"].(float64); ok && d <= 0 {
				continue
			}
		}
		present[s.Stage] = true
	}
	var out []string
	for _, st := range StageOrder {
		if present[st] {
			out = append(out, st)
		}
	}
	return out
}

// CanEnter reports whether a participant may enter targetStage given the set
// of completed stages. All enabled stages strictly before targetStage must be
// complete; non-enabled stages are skipped (D5 sequential gating, SS3-03).
func (f *Flow) CanEnter(targetStage string, done map[string]bool) bool {
	if targetStage == "" {
		return true // auxiliary steps are not gated
	}
	for _, st := range f.EnabledStages() {
		if st == targetStage {
			return true
		}
		if !done[st] {
			return false
		}
	}
	return true // targetStage not an enabled stage → ungated
}
