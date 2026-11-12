package lens

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

const meta = `{"type":"session_meta","payload":{"id":"test-synthetic"}}` + "\n"

func event(kind, id, name, ts string) string {
	return fmt.Sprintf(`{"type":"response_item","timestamp":%q,"payload":{"type":%q,"call_id":%q,"name":%q}}`+"\n", ts, kind, id, name)
}
func call(id, name, ts string) string { return event("function_call", id, name, ts) }
func result(id, ts string) string     { return event("function_call_output", id, "", ts) }

const end = `{"type":"event_msg","payload":{"type":"task_complete"}}` + "\n"

func TestIntervals(t *testing.T) {
	// Synthetic: 3s and 4s overlapping calls; sum=7, union=5, p95=4.
	input := meta + call("a", "shell", "2026-01-01T00:00:00Z") + call("b", "shell", "2026-01-01T00:00:01Z") + result("a", "2026-01-01T00:00:03Z") + result("b", "2026-01-01T00:00:05Z") + end
	s, err := Analyze(strings.NewReader(input), "test")
	if err != nil {
		t.Fatal(err)
	}
	if *s.Active != 5 || *s.Groups[0].Total != 7 || *s.Groups[0].P95 != 4 || !s.Complete {
		t.Fatalf("unexpected: %+v", s)
	}
	if s.Calls[0].StartLine != 2 || *s.Calls[0].EndLine != 4 {
		t.Fatal("wrong evidence addresses")
	}
	// Same durations, sequential: union must now equal sum.
	input = meta + call("a", "shell", "2026-01-01T00:00:00Z") + result("a", "2026-01-01T00:00:03Z") + call("b", "shell", "2026-01-01T00:00:03Z") + result("b", "2026-01-01T00:00:07Z") + end
	s, err = Analyze(strings.NewReader(input), "test")
	if err != nil || *s.Active != 7 {
		t.Fatalf("sequential: %+v %v", s, err)
	}
}
func TestUnknownIsNotZero(t *testing.T) {
	for _, suffix := range []string{"", result("a", ""), result("a", "2025-01-01T00:00:00Z")} {
		s, err := Analyze(strings.NewReader(meta+call("a", "shell", "2026-01-01T00:00:00Z")+suffix), "test")
		if err != nil {
			t.Fatal(err)
		}
		if s.Calls[0].Seconds != nil || s.Groups[0].Total != nil || s.Active != nil || len(s.Warnings) == 0 {
			t.Fatal("unknown replaced with zero")
		}
	}
}
func TestLinkByIDNotOrder(t *testing.T) {
	s, err := Analyze(strings.NewReader(meta+call("a", "one", "2026-01-01T00:00:00Z")+call("b", "two", "2026-01-01T00:00:01Z")+result("b", "2026-01-01T00:00:02Z")+result("a", "2026-01-01T00:00:04Z")+end), "test")
	if err != nil {
		t.Fatal(err)
	}
	if *s.Calls[0].Seconds != 4 || *s.Calls[1].Seconds != 1 {
		t.Fatal("incorrect pairing")
	}
}
func TestBadInput(t *testing.T) {
	for _, input := range []string{"", `{`, "null\n", `{"type":"thread.started"}` + "\n", meta + meta, meta + call("a", "x", "") + call("a", "x", "")} {
		if _, err := Analyze(strings.NewReader(input), "test"); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}
func TestEmptyAndOrphan(t *testing.T) {
	s, err := Analyze(strings.NewReader(meta+result("missing", "")+end), "test")
	if err != nil || len(s.Calls) != 0 || len(s.Warnings) != 2 {
		t.Fatalf("%+v %v", s, err)
	}
}
func TestPrepare(t *testing.T) {
	input := meta + `{"type":"response_item","timestamp":"2026-01-01T00:00:00Z","payload":{"type":"custom_tool_call","call_id":"a","name":"exec","input":"SECRET"}}` + "\n" + result("a", "2026-01-01T00:00:01Z") + end
	var out bytes.Buffer
	if err := Prepare(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "SECRET") || !strings.Contains(out.String(), `"source_line":2`) {
		t.Fatal(out.String())
	}
	s, err := Analyze(&out, "prepared")
	if err != nil || *s.Calls[0].Seconds != 1 {
		t.Fatalf("%+v %v", s, err)
	}
}
func TestNewTurnResetsCompletion(t *testing.T) {
	s, err := Analyze(strings.NewReader(meta+end+`{"type":"event_msg","payload":{"type":"task_started"}}`+"\n"), "test")
	if err != nil || s.Complete {
		t.Fatal("stale completion")
	}
}
