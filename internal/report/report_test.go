package report

import (
	"bytes"
	"strings"
	"testing"

	"agentlens/internal/lens"
)

func TestSeparateMCPTable(t *testing.T) {
	var out bytes.Buffer
	printTools(&out, []lens.Group{{Tool: "exec", Calls: 2}, {Tool: "mcp__docs__search", Calls: 1, Success: 1}})
	parts := strings.Split(out.String(), "MCP TOOLS")
	if len(parts) != 2 || strings.Contains(parts[0], "search") || !strings.Contains(parts[1], "search") || strings.Contains(parts[1], "lookup") {
		t.Fatal(out.String())
	}
	if strings.Contains(out.String(), "Время инструментов —") {
		t.Fatal("verbose explanation")
	}
}

func TestTimingAndTokensVisible(t *testing.T) {
	sec, p50, p95 := 7.0, 3.0, 4.0
	total, input, output, cached := int64(120), int64(100), int64(20), int64(40)
	r := lens.Report{SessionCount: 1, Sessions: []lens.Session{{File: "test.jsonl", Active: &sec,
		Groups:  []lens.Group{{Tool: "exec", Calls: 2, Measured: 2, Total: &sec, P50: &p50, P95: &p95}},
		Metrics: lens.Metrics{Tokens: &lens.Tokens{Total: &total, Usage: lens.Usage{Input: &input, Output: &output, Cached: &cached}}},
	}}}
	var out bytes.Buffer
	Print(&out, r, false)
	for _, want := range []string{"p50", "p95", "7.000 s", "3.000 s", "4.000 s", "120", "100 / 20", "Токены всего"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}
