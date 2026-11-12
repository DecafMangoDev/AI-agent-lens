package lens

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func wrapperResult(id string, code int) string {
	process, _ := json.Marshal(map[string]any{"chunk_id": "chunk", "exit_code": code, "output": "same error"})
	b, _ := json.Marshal(map[string]any{"type": "response_item", "timestamp": "2026-01-01T00:00:01Z", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": id, "output": []any{map[string]string{"type": "input_text", "text": "Script completed\nWall time 1 seconds\nOutput:\n"}, map[string]string{"type": "input_text", "text": string(process)}}}})
	return string(b) + "\n"
}
func wrapperCall(id, input string) string {
	b, _ := json.Marshal(map[string]any{"type": "response_item", "timestamp": "2026-01-01T00:00:00Z", "payload": map[string]string{"type": "custom_tool_call", "call_id": id, "name": "exec", "input": input}})
	return string(b) + "\n"
}
func TestWrapperAndObservations(t *testing.T) {
	input := meta + wrapperCall("a", "same input") + wrapperResult("a", 1) + wrapperCall("b", "same input") + wrapperResult("b", 1) + end
	var exported bytes.Buffer
	if e := Prepare(strings.NewReader(input), &exported); e != nil {
		t.Fatal(e)
	}
	for _, src := range []string{input, exported.String()} {
		s, e := Analyze(strings.NewReader(src), "test")
		if e != nil {
			t.Fatal(e)
		}
		if s.Groups[0].Success != 2 || s.Groups[0].Failure != 0 || len(s.Observations) != 2 || s.Observations[0].Kind != "repeated_failure" || s.Observations[1].Kind != "hidden_process_failure" {
			t.Fatalf("%+v", s)
		}
		if *s.Calls[0].ProcessResults[0].ExitCode != 1 || s.Calls[0].ProcessResults[0].Block != 2 {
			t.Fatal("missing evidence")
		}
	}
}
func TestDifferentInputsNotSameAttempt(t *testing.T) {
	s, e := Analyze(strings.NewReader(meta+wrapperCall("a", `cwd=one`)+wrapperResult("a", 1)+wrapperCall("b", `cwd=two`)+wrapperResult("b", 1)+end), "test")
	if e != nil {
		t.Fatal(e)
	}
	for _, o := range s.Observations {
		if o.Kind == "repeated_failure" {
			t.Fatal("merged different conditions")
		}
	}
}
func TestNoResultNoInventedFailure(t *testing.T) {
	s, e := Analyze(strings.NewReader(meta+wrapperCall("a", "test")), "test")
	if e != nil {
		t.Fatal(e)
	}
	if s.Observations[0].Kind != "insufficient_evidence" || s.Calls[0].Outcome != "pending" {
		t.Fatal(s)
	}
}
