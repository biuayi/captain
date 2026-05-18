package flow

import "testing"

const good = `{"version":1,"flowId":"f","name":"n","entryStepId":"s1",
"steps":[
 {"id":"s1","type":"checkin","nextStepId":"s2","config":{}},
 {"id":"s2","type":"result","nextStepId":null,"config":{}}]}`

func TestParseGood(t *testing.T) {
	f, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Step("s2"); !ok {
		t.Fatal("s2 missing")
	}
}

func TestRejectsBad(t *testing.T) {
	cases := map[string]string{
		"unknown type":  `{"entryStepId":"s1","steps":[{"id":"s1","type":"bogus"}]}`,
		"dup id":        `{"entryStepId":"s1","steps":[{"id":"s1","type":"checkin"},{"id":"s1","type":"result"}]}`,
		"bad entry":     `{"entryStepId":"x","steps":[{"id":"s1","type":"checkin"}]}`,
		"dangling next": `{"entryStepId":"s1","steps":[{"id":"s1","type":"checkin","nextStepId":"zzz"}]}`,
		"no steps":      `{"entryStepId":"s1","steps":[]}`,
	}
	for name, js := range cases {
		if _, err := Parse([]byte(js)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

const v2 = `{"version":2,"flowId":"f","name":"n","entryStepId":"s1","steps":[
 {"id":"s1","type":"checkin","stage":"R1","nextStepId":"s2","config":{"days":2}},
 {"id":"s2","type":"form","stage":"R2","nextStepId":"s3","config":{"fields":[]}},
 {"id":"s3","type":"exam","stage":"R3","nextStepId":"s4","config":{"mode":"random","randomCount":5}},
 {"id":"s4","type":"lottery","stage":"R4","nextStepId":null,"config":{"drawLimit":1}}]}`

func TestV2StagesAndGating(t *testing.T) {
	f, err := Parse([]byte(v2))
	if err != nil {
		t.Fatalf("v2 parse: %v", err)
	}
	if got := f.EnabledStages(); len(got) != 4 {
		t.Fatalf("enabled stages = %v want R1-R4", got)
	}
	// R3 blocked until R1+R2 done
	if f.CanEnter("R3", map[string]bool{"R1": true}) {
		t.Fatal("R3 should be gated until R2 done")
	}
	if !f.CanEnter("R3", map[string]bool{"R1": true, "R2": true}) {
		t.Fatal("R3 should open after R1+R2")
	}
	if !f.CanEnter("", map[string]bool{}) {
		t.Fatal("auxiliary (no stage) must be ungated")
	}
}

func TestV2StageTypeBinding(t *testing.T) {
	bad := `{"entryStepId":"s1","steps":[{"id":"s1","type":"form","stage":"R1","config":{"fields":[]}}]}`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("R1 must require checkin type")
	}
	examBad := `{"entryStepId":"s1","steps":[{"id":"s1","type":"exam","stage":"R3","config":{"mode":"bogus"}}]}`
	if _, err := Parse([]byte(examBad)); err == nil {
		t.Fatal("exam.mode must be all|random")
	}
}

func TestR1DisabledWhenZeroDays(t *testing.T) {
	js := `{"entryStepId":"s1","steps":[
	 {"id":"s1","type":"checkin","stage":"R1","nextStepId":"s2","config":{"days":0}},
	 {"id":"s2","type":"form","stage":"R2","config":{"fields":[]}}]}`
	f, err := Parse([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	got := f.EnabledStages()
	if len(got) != 1 || got[0] != "R2" {
		t.Fatalf("days=0 should disable R1; enabled=%v", got)
	}
}
