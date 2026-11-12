package lens

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Signals never retains arguments or output content.
type Signals struct {
	References  []string `json:"references"`
	Fingerprint string   `json:"fingerprint,omitempty"`
}
type Usage struct {
	Input     *int64 `json:"input_tokens"`
	Cached    *int64 `json:"cached_input_tokens"`
	Output    *int64 `json:"output_tokens"`
	Reasoning *int64 `json:"reasoning_output_tokens"`
}
type Tokens struct {
	Total *int64 `json:"total_tokens"`
	Usage
	Records int   `json:"records"`
	Lines   []int `json:"lines"`
}
type Reference struct {
	Tool     string `json:"tool"`
	Server   string `json:"mcp_server,omitempty"`
	Mentions int    `json:"mentions"`
	Lines    []int  `json:"lines"`
}
type Metrics struct {
	Tokens          *Tokens     `json:"tokens"`
	References      []Reference `json:"tool_references"`
	InspectedCalls  int         `json:"inspected_calls"`
	RepeatedInputs  *int        `json:"repeated_inputs"`
	ResultBytes     *int64      `json:"result_json_bytes"`
	MeasuredResults int         `json:"measured_results"`
	PeakParallel    *int        `json:"peak_parallel"`
	ToolWindow      *float64    `json:"tool_window_seconds"`
}

func MCPServer(name string) string {
	name = strings.TrimPrefix(name, "functions.")
	if !strings.HasPrefix(name, "mcp__") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(name, "mcp__"), "__")
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}

// References are static lexical mentions, NOT proof that an inner tool executed.
// String literals, templates and comments are discarded. Dynamic calls are omitted.
func toolReferences(code string) []string {
	tokens := []string{}
	for i := 0; i < len(code); {
		c := code[i]
		if c == '\'' || c == '"' || c == '`' {
			quote := c
			i++
			for i < len(code) {
				if code[i] == '\\' {
					i += 2
					continue
				}
				if code[i] == quote {
					i++
					break
				}
				i++
			}
			tokens = append(tokens, "literal")
			continue
		}
		if i+1 < len(code) && code[i:i+2] == "//" {
			for i < len(code) && code[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(code) && code[i:i+2] == "/*" {
			i += 2
			for i+1 < len(code) && code[i:i+2] != "*/" {
				i++
			}
			if i+1 < len(code) {
				i += 2
			}
			continue
		}
		if unicode.IsSpace(rune(c)) {
			i++
			continue
		}
		if c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			j := i + 1
			for j < len(code) {
				v := code[j]
				if !(v == '_' || v == '$' || v >= 'a' && v <= 'z' || v >= 'A' && v <= 'Z' || v >= '0' && v <= '9') {
					break
				}
				j++
			}
			tokens = append(tokens, code[i:j])
			i = j
			continue
		}
		tokens = append(tokens, string(c))
		i++
	}
	out := []string{}
	for i := 0; i+3 < len(tokens); i++ {
		if tokens[i] == "tools" && tokens[i+1] == "." && tokens[i+3] == "(" {
			out = append(out, tokens[i+2])
		}
	}
	return out
}
func callSignals(rec Record, p Payload) *Signals {
	if p.Signals != nil {
		return p.Signals
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(rec.Payload, &raw)
	field := raw["arguments"]
	if p.Type == "custom_tool_call" {
		field = raw["input"]
	}
	if len(field) == 0 {
		return nil
	}
	// Canonicalize JSON argument objects but preserve string contents exactly.
	var v any
	if json.Unmarshal(field, &v) != nil {
		return nil
	}
	canonical, _ := json.Marshal(v)
	h := sha256.Sum256(append([]byte(p.Name+"\x00"), canonical...))
	s := &Signals{Fingerprint: hex.EncodeToString(h[:]), References: []string{}}
	if p.Name == "exec" || p.Name == "functions.exec" {
		if code, ok := v.(string); ok {
			s.References = toolReferences(code)
		}
	}
	return s
}
func resultSize(rec Record, p Payload) *int64 {
	if p.ResultBytes != nil {
		return p.ResultBytes
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(rec.Payload, &raw)
	v, ok := raw["output"]
	if !ok {
		return nil
	}
	n := int64(len(v))
	return &n
}
func sumKnown(dst **int64, v *int64, first bool) {
	if first {
		if v != nil {
			n := *v
			*dst = &n
		}
		return
	}
	if *dst == nil || v == nil {
		*dst = nil
		return
	}
	**dst += *v
}

type observer struct {
	tokens *Tokens
	seen   map[string]bool
}

func (o *observer) record(rec Record, line int) {
	if rec.Type != "token_usage_record" {
		return
	}
	var p struct {
		ResponseID string `json:"response_id"`
		Usage      *Usage `json:"usage"`
	}
	if json.Unmarshal(rec.Payload, &p) != nil || p.Usage == nil || p.ResponseID == "" {
		return
	}
	if o.seen == nil {
		o.seen = map[string]bool{}
	}
	if o.seen[p.ResponseID] {
		return
	}
	o.seen[p.ResponseID] = true
	if o.tokens == nil {
		o.tokens = &Tokens{Lines: []int{}}
	}
	t := o.tokens
	first := t.Records == 0
	sumKnown(&t.Input, p.Usage.Input, first)
	sumKnown(&t.Cached, p.Usage.Cached, first)
	sumKnown(&t.Output, p.Usage.Output, first)
	sumKnown(&t.Reasoning, p.Usage.Reasoning, first)
	t.Total = nil
	if t.Input != nil && t.Output != nil {
		n := *t.Input + *t.Output
		t.Total = &n
	}
	t.Records++
	t.Lines = append(t.Lines, line)
}
func measure(calls []Evidence, tokens *Tokens) Metrics {
	m := Metrics{Tokens: tokens, References: []Reference{}}
	refs := map[string]*Reference{}
	fingerprints := map[string]bool{}
	repeat := 0
	type point struct {
		t     time.Time
		delta int
	}
	points := []point{}
	var first, last time.Time
	for _, e := range calls {
		if e.Signals != nil {
			m.InspectedCalls++
			if e.Signals.Fingerprint != "" {
				if fingerprints[e.Signals.Fingerprint] {
					repeat++
				}
				fingerprints[e.Signals.Fingerprint] = true
			}
			for _, name := range e.Signals.References {
				r := refs[name]
				if r == nil {
					r = &Reference{Tool: name, Server: MCPServer(name), Lines: []int{}}
					refs[name] = r
				}
				r.Mentions++
				r.Lines = append(r.Lines, e.StartLine)
			}
		}
		if e.ResultBytes != nil {
			if m.ResultBytes == nil {
				v := int64(0)
				m.ResultBytes = &v
			}
			*m.ResultBytes += *e.ResultBytes
			m.MeasuredResults++
		}
		if e.Seconds != nil {
			a, _ := time.Parse(time.RFC3339Nano, e.Start)
			b, _ := time.Parse(time.RFC3339Nano, *e.End)
			if first.IsZero() || a.Before(first) {
				first = a
			}
			if last.IsZero() || b.After(last) {
				last = b
			}
			if b.After(a) {
				points = append(points, point{a, 1}, point{b, -1})
			}
		}
	}
	if m.InspectedCalls > 0 {
		m.RepeatedInputs = &repeat
	}
	for _, r := range refs {
		m.References = append(m.References, *r)
	}
	sort.Slice(m.References, func(i, j int) bool { return m.References[i].Tool < m.References[j].Tool })
	if !first.IsZero() {
		v := last.Sub(first).Seconds()
		m.ToolWindow = &v
		peak, active := 0, 0
		sort.Slice(points, func(i, j int) bool {
			if points[i].t.Equal(points[j].t) {
				return points[i].delta < points[j].delta
			}
			return points[i].t.Before(points[j].t)
		})
		for _, p := range points {
			active += p.delta
			if active > peak {
				peak = active
			}
		}
		m.PeakParallel = &peak
	}
	return m
}
