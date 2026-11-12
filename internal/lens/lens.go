// Package lens measures observable tool-call intervals in Codex rollout JSONL.
package lens

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

const MaxLine = 16 * 1024 * 1024

type Record struct {
	Timestamp  string          `json:"timestamp,omitempty"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	SourceLine int             `json:"source_line,omitempty"`
}
type Payload struct {
	ProcessResults  []ProcessResult `json:"process_results,omitempty"`
	OutputInspected bool            `json:"output_inspected,omitempty"`
	Outcome         string          `json:"outcome,omitempty"`
	Type            string          `json:"type,omitempty"`
	ID              string          `json:"id,omitempty"`
	SessionID       string          `json:"session_id,omitempty"`
	CallID          string          `json:"call_id,omitempty"`
	Name            string          `json:"name,omitempty"`
	Signals         *Signals        `json:"signals,omitempty"`
	ResultBytes     *int64          `json:"result_json_bytes,omitempty"`
	Usage           *Usage          `json:"usage,omitempty"`
	ResponseID      string          `json:"response_id,omitempty"`
}
type Evidence struct {
	ProcessResults  []ProcessResult `json:"process_results"`
	OutputInspected bool            `json:"output_inspected"`
	Outcome         string          `json:"outcome"`
	Signals         *Signals        `json:"signals,omitempty"`
	ResultBytes     *int64          `json:"result_json_bytes"`
	CallID          string          `json:"call_id"`
	Tool            string          `json:"tool"`
	StartLine       int             `json:"start_line"`
	EndLine         *int            `json:"end_line"`
	Start           string          `json:"start"`
	End             *string         `json:"end"`
	Seconds         *float64        `json:"seconds"`
}
type Group struct {
	Success  int      `json:"success"`
	Failure  int      `json:"failure"`
	Unknown  int      `json:"unknown"`
	Pending  int      `json:"pending"`
	Tool     string   `json:"tool"`
	Calls    int      `json:"calls"`
	Measured int      `json:"measured"`
	Total    *float64 `json:"total_seconds"`
	P50      *float64 `json:"p50_seconds"`
	P95      *float64 `json:"p95_seconds"`
}
type Session struct {
	Observations []Observation `json:"observations"`
	Metrics      Metrics       `json:"metrics"`
	ID           string        `json:"session_id"`
	File         string        `json:"file"`
	Complete     bool          `json:"complete"`
	Calls        []Evidence    `json:"calls"`
	Groups       []Group       `json:"groups"`
	Active       *float64      `json:"active_seconds"`
	Warnings     []string      `json:"warnings"`
}
type FileError struct {
	File  string `json:"file"`
	Error string `json:"error"`
}
type AIReview struct {
	Model    string `json:"model"`
	Text     string `json:"text"`
	Error    string `json:"error,omitempty"`
	Artifact string `json:"artifact"`
}
type Report struct {
	AI           *AIReview   `json:"ai,omitempty"`
	Schema       string      `json:"schema"`
	Complete     bool        `json:"complete"`
	Sessions     []Session   `json:"sessions"`
	SessionCount int         `json:"session_count"`
	Groups       []Group     `json:"groups"`
	Errors       []FileError `json:"errors"`
}

func Read(r io.Reader, visit func(Record, Payload, int) error) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), MaxLine)
	n := 0
	for s.Scan() {
		n++
		if len(s.Bytes()) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(s.Bytes(), &rec); err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
		if rec.Type == "" || len(rec.Payload) == 0 || string(rec.Payload) == "null" {
			return fmt.Errorf("line %d: expected Codex rollout record with type and payload", n)
		}
		var p Payload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return fmt.Errorf("line %d: invalid payload: %w", n, err)
		}
		if err := visit(rec, p, n); err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("after line %d: %w (maximum line size %d)", n, err, MaxLine)
	}
	return nil
}
func IsCall(t string) bool   { return t == "function_call" || t == "custom_tool_call" }
func IsResult(t string) bool { return t == "function_call_output" || t == "custom_tool_call_output" }
func Analyze(r io.Reader, file string) (Session, error) {
	s := Session{File: file, Calls: []Evidence{}, Groups: []Group{}, Warnings: []string{}}
	pending := map[string]int{}
	seen := map[string]bool{}
	meta := false
	obs := observer{}
	err := Read(r, func(rec Record, p Payload, line int) error {
		obs.record(rec, line)
		switch rec.Type {
		case "session_meta":
			if meta {
				return fmt.Errorf("multiple session_meta records; one file must contain one session")
			}
			meta = true
			s.ID = p.ID
			if s.ID == "" {
				s.ID = p.SessionID
			}
			if s.ID == "" {
				return fmt.Errorf("session_meta has no id")
			}
		case "event_msg":
			switch p.Type {
			case "task_started", "turn_aborted":
				s.Complete = false
			case "task_complete", "task_completed":
				s.Complete = true
			}
		case "response_item":
			if IsCall(p.Type) {
				if p.CallID == "" || p.Name == "" {
					return fmt.Errorf("tool call requires call_id and name")
				}
				if seen[p.CallID] {
					return fmt.Errorf("duplicate call_id %q", p.CallID)
				}
				seen[p.CallID] = true
				pending[p.CallID] = len(s.Calls)
				s.Calls = append(s.Calls, Evidence{CallID: p.CallID, Tool: p.Name, StartLine: line, Start: rec.Timestamp, Signals: callSignals(rec, p), Outcome: "pending"})
			} else if IsResult(p.Type) {
				if p.CallID == "" {
					return fmt.Errorf("tool result requires call_id")
				}
				i, ok := pending[p.CallID]
				if !ok {
					s.Warnings = append(s.Warnings, fmt.Sprintf("line %d: result %s has no pending call", line, p.CallID))
					return nil
				}
				delete(pending, p.CallID)
				e := &s.Calls[i]
				e.Outcome = resultOutcome(rec, p)
				if e.Tool == "exec" || e.Tool == "functions.exec" {
					if state := wrapperOutcome(rec); state != "unknown" {
						e.Outcome = state
					}
				}
				e.ProcessResults, e.OutputInspected = processResults(rec, p)
				e.ResultBytes = resultSize(rec, p)
				e.EndLine = &line
				e.End = &rec.Timestamp
				a, ea := time.Parse(time.RFC3339Nano, e.Start)
				b, eb := time.Parse(time.RFC3339Nano, rec.Timestamp)
				if ea != nil || eb != nil {
					s.Warnings = append(s.Warnings, fmt.Sprintf("call %s: missing or invalid timestamp; duration unknown", p.CallID))
				} else if b.Before(a) {
					s.Warnings = append(s.Warnings, fmt.Sprintf("call %s: result timestamp precedes call; duration unknown", p.CallID))
				} else {
					v := b.Sub(a).Seconds()
					e.Seconds = &v
				}
			}
		}
		return nil
	})
	if err != nil {
		return s, err
	}
	if !meta {
		return s, fmt.Errorf("missing session_meta: expected Codex rollout JSONL, not codex exec --json")
	}
	for _, e := range s.Calls {
		if e.EndLine == nil {
			s.Warnings = append(s.Warnings, "call "+e.CallID+": missing result; duration unknown")
		}
	}
	if !s.Complete {
		s.Warnings = append(s.Warnings, "no terminal task_complete after the latest task start; session may be interrupted")
	}
	if len(s.Calls) == 0 {
		s.Warnings = append(s.Warnings, "no tool calls; tool latency cannot be assessed")
	}
	s.Groups = Groups(s.Calls)
	s.Active = Union(s.Calls)
	s.Metrics = measure(s.Calls, obs.tokens)
	s.Observations = observations(s.Calls)
	return s, nil
}
func Groups(calls []Evidence) []Group {
	by := map[string][]float64{}
	counts := map[string]int{}
	outcomes := map[string]Group{}
	for _, e := range calls {
		counts[e.Tool]++
		g := outcomes[e.Tool]
		switch e.Outcome {
		case "success":
			g.Success++
		case "failure":
			g.Failure++
		case "pending":
			g.Pending++
		default:
			g.Unknown++
		}
		outcomes[e.Tool] = g
		if e.Seconds != nil {
			by[e.Tool] = append(by[e.Tool], *e.Seconds)
		}
	}
	out := []Group{}
	for tool, count := range counts {
		v := by[tool]
		g := outcomes[tool]
		g.Tool = tool
		g.Calls = count
		g.Measured = len(v)
		if len(v) > 0 {
			sort.Float64s(v)
			sum := 0.0
			for _, x := range v {
				sum += x
			}
			a := v[(len(v)-1)/2]
			b := v[(95*len(v)+99)/100-1]
			g.Total = &sum
			g.P50 = &a
			g.P95 = &b
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}

// Union counts overlapping tool intervals once. Unknown intervals are excluded.
func Union(calls []Evidence) *float64 {
	type interval struct{ a, b time.Time }
	is := []interval{}
	for _, e := range calls {
		if e.Seconds != nil {
			a, _ := time.Parse(time.RFC3339Nano, e.Start)
			b, _ := time.Parse(time.RFC3339Nano, *e.End)
			is = append(is, interval{a, b})
		}
	}
	if len(is) == 0 {
		return nil
	}
	sort.Slice(is, func(i, j int) bool { return is[i].a.Before(is[j].a) })
	a, b := is[0].a, is[0].b
	total := 0.0
	for _, v := range is[1:] {
		if v.a.After(b) {
			total += b.Sub(a).Seconds()
			a, b = v.a, v.b
		} else if v.b.After(b) {
			b = v.b
		}
	}
	total += b.Sub(a).Seconds()
	return &total
}

// Prepare exports only fields needed for latency analysis; it never copies prompts,
// tool arguments, outputs, credentials, account metadata or model reasoning.
func Prepare(r io.Reader, w io.Writer) error {
	enc := json.NewEncoder(w)
	names := map[string]string{}
	return Read(r, func(rec Record, p Payload, line int) error {
		keep := rec.Type == "token_usage_record" || rec.Type == "session_meta" || (rec.Type == "response_item" && (IsCall(p.Type) || IsResult(p.Type))) || (rec.Type == "event_msg" && (p.Type == "task_started" || p.Type == "task_complete" || p.Type == "task_completed" || p.Type == "turn_aborted"))
		if !keep {
			return nil
		}
		if rec.SourceLine == 0 {
			rec.SourceLine = line
		}
		if IsCall(p.Type) {
			p.Signals = callSignals(rec, p)
			names[p.CallID] = p.Name
		}
		if IsResult(p.Type) {
			p.ResultBytes = resultSize(rec, p)
			p.Outcome = resultOutcome(rec, p)
			if names[p.CallID] == "exec" || names[p.CallID] == "functions.exec" {
				if state := wrapperOutcome(rec); state != "unknown" {
					p.Outcome = state
				}
			}
			p.ProcessResults, p.OutputInspected = processResults(rec, p)
		}
		b, err := json.Marshal(p)
		if err != nil {
			return err
		}
		rec.Payload = b
		return enc.Encode(rec)
	})
}
