package lens

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestResultOutcome(t *testing.T) {
	for _, tc := range []struct {
		output string
		want   string
	}{
		{`{"exit_code":0}`, "success"}, {`{"exit_code":2}`, "failure"},
		{`{"isError":false}`, "success"}, {`{"isError":true}`, "failure"},
		{`{"isError":false,"exit_code":1}`, "failure"}, {`{"exit_code":0,"timed_out":true}`, "failure"},
		{`"{\"exit_code\":0}"`, "success"}, {`"Process exited with code 137\nOutput:\n"`, "failure"},
		{`"Process exited with code 0\nOutput:\n"`, "success"},
		{`"Script completed\n"`, "unknown"}, {`"some text contains error"`, "unknown"},
		{`[{"text":"{\"exit_code\":1}"}]`, "unknown"}, {`null`, "unknown"},
	} {
		p := []byte(`{"type":"function_call_output","call_id":"a","output":` + tc.output + `}`)
		got := resultOutcome(Record{Payload: p}, Payload{})
		if got != tc.want {
			t.Errorf("%s: %s != %s", tc.output, got, tc.want)
		}
	}
}
func TestOutcomeAggregationAndExport(t *testing.T) {
	input := meta
	for i, out := range []string{`{"exit_code":0}`, `{"isError":true}`, `"plain text"`} {
		id := string(rune('a' + i))
		input += call(id, "shell", "2026-01-01T00:00:00Z")
		p := map[string]any{"type": "response_item", "timestamp": "2026-01-01T00:00:01Z", "payload": map[string]any{"type": "function_call_output", "call_id": id, "output": json.RawMessage(out)}}
		b, _ := json.Marshal(p)
		input += string(b) + "\n"
	}
	input += call("pending", "shell", "2026-01-01T00:00:02Z") + end
	var prepared bytes.Buffer
	if err := Prepare(strings.NewReader(input), &prepared); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{input, prepared.String()} {
		s, err := Analyze(strings.NewReader(src), "test")
		if err != nil {
			t.Fatal(err)
		}
		g := s.Groups[0]
		if g.Success != 1 || g.Failure != 1 || g.Unknown != 1 || g.Pending != 1 {
			t.Fatalf("%+v", g)
		}
	}
}
func TestTotalTokensExcludesSubsets(t *testing.T) {
	o := observer{}
	o.record(Record{Type: "token_usage_record", Payload: []byte(`{"response_id":"a","usage":{"input_tokens":100,"cached_input_tokens":80,"output_tokens":20,"reasoning_output_tokens":10}}`)}, 1)
	if o.tokens.Total == nil || *o.tokens.Total != 120 {
		t.Fatalf("%+v", o.tokens)
	}
}
