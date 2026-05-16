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
