package lens

import (
	"bytes"
	"strings"
	"testing"
)

func TestMetrics(t *testing.T) {
	code := `{"type":"response_item","timestamp":"2026-01-01T00:00:00Z","payload":{"type":"custom_tool_call","call_id":"a","name":"exec","input":"await tools.exec_command({}); await tools.mcp__docs__search({}); // tools.fake()\n const x = 'tools.secret()';"}}` + "\n"
	usage := `{"type":"token_usage_record","payload":{"response_id":"r1","usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":20,"reasoning_output_tokens":5}}}` + "\n"
	input := meta + code + `{"type":"response_item","timestamp":"2026-01-01T00:00:01Z","payload":{"type":"custom_tool_call_output","call_id":"a","output":"ok"}}` + "\n" + usage + usage + end
	s, e := Analyze(strings.NewReader(input), "test")
	if e != nil {
		t.Fatal(e)
	}
	m := s.Metrics
	if len(m.References) != 2 || m.References[1].Server != "docs" || *m.Tokens.Input != 100 || m.Tokens.Records != 1 || *m.ResultBytes != 4 || *m.PeakParallel != 1 {
		t.Fatalf("%+v", m)
	}
	var b bytes.Buffer
	if e = Prepare(strings.NewReader(input), &b); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(b.String(), "tools.secret") || strings.Contains(b.String(), `"output":"ok"`) {
		t.Fatal("export leaked contents")
	}
	prepared, e := Analyze(&b, "export")
	if e != nil || len(prepared.Metrics.References) != 2 || *prepared.Metrics.Tokens.Input != 100 {
		t.Fatalf("%+v %v", prepared, e)
	}
}
func TestRepeatedAndUnknown(t *testing.T) {
	c := `{"type":"response_item","payload":{"type":"function_call","call_id":"a","name":"shell","arguments":"same"}}` + "\n"
	s, e := Analyze(strings.NewReader(meta+c+strings.Replace(c, `"a"`, `"b"`, 1)+end), "test")
	if e != nil || *s.Metrics.RepeatedInputs != 1 {
		t.Fatalf("%+v %v", s, e)
	}
	s, e = Analyze(strings.NewReader(meta+end), "test")
	if e != nil || s.Metrics.RepeatedInputs != nil || s.Metrics.Tokens != nil || s.Metrics.ResultBytes != nil {
		t.Fatal("unknown became zero")
	}
}
func TestReferencesAreNotExecutions(t *testing.T) {
	refs := toolReferences("// tools.fake()\n/* tools.fake() */ `tools.fake()`; 'tools.fake()'; if(false){tools.mcp__x__y({})}; tools[name]({})")
	if len(refs) != 1 || refs[0] != "mcp__x__y" {
		t.Fatal(refs)
	}
}
func TestPeakParallelAndMissingUsage(t *testing.T) {
	s, e := Analyze(strings.NewReader(meta+call("a", "one", "2026-01-01T00:00:00Z")+call("b", "two", "2026-01-01T00:00:01Z")+result("b", "2026-01-01T00:00:02Z")+result("a", "2026-01-01T00:00:03Z")+end), "test")
	if e != nil || *s.Metrics.PeakParallel != 2 || *s.Metrics.ToolWindow != 3 {
		t.Fatalf("%+v %v", s, e)
	}
	o := observer{}
	o.record(Record{Type: "token_usage_record", Payload: []byte(`{"response_id":"a","usage":{"input_tokens":1}}`)}, 1)
	o.record(Record{Type: "token_usage_record", Payload: []byte(`{"response_id":"b","usage":{"output_tokens":2}}`)}, 2)
	if o.tokens.Input != nil || o.tokens.Output != nil {
		t.Fatal("partial usage treated as total")
	}
}
