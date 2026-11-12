package lens

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ProcessResult is a printed process result, not an inferred inner tool call.
// Block is the 1-based output content block; zero means the output object itself.
type ProcessResult struct {
	Block      int    `json:"block"`
	ExitCode   *int   `json:"exit_code"`
	Running    bool   `json:"running"`
	OutputHash string `json:"output_sha256,omitempty"`
}
type Observation struct {
	Kind    string   `json:"kind"`
	Fact    string   `json:"fact"`
	Action  string   `json:"action"`
	CallIDs []string `json:"call_ids"`
	Lines   []int    `json:"lines"`
}

func outputBlocks(rec Record) []string {
	var p struct {
		Output json.RawMessage `json:"output"`
	}
	if json.Unmarshal(rec.Payload, &p) != nil {
		return nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(p.Output, &blocks) != nil {
		return nil
	}
	out := make([]string, len(blocks))
	for i, b := range blocks {
		if b.Type == "input_text" || b.Type == "text" {
			out[i] = b.Text
		}
	}
	return out
}
func wrapperOutcome(rec Record) string {
	blocks := outputBlocks(rec)
	if len(blocks) == 0 {
		return "unknown"
	}
	// Only the harness's first block, never arbitrary nested command output.
	if strings.HasPrefix(blocks[0], "Script completed\nWall time ") && strings.Contains(blocks[0], "\nOutput:\n") {
		return "success"
	}
	if strings.HasPrefix(blocks[0], "Script failed\n") {
		return "failure"
	}
	return "unknown"
}
func processResults(rec Record, p Payload) ([]ProcessResult, bool) {
	if p.OutputInspected || p.ProcessResults != nil {
		return p.ProcessResults, true
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(rec.Payload, &raw)
	if _, ok := raw["output"]; !ok {
		return []ProcessResult{}, false
	}
	found := []ProcessResult{}
	parse := func(data []byte, block int) {
		var envelope struct {
			Status string          `json:"status"`
			Value  json.RawMessage `json:"value"`
		}
		_ = json.Unmarshal(data, &envelope)
		if envelope.Status == "fulfilled" && len(envelope.Value) > 0 {
			data = envelope.Value
		}
		var v struct {
			Chunk   string          `json:"chunk_id"`
			Exit    *int            `json:"exit_code"`
			Session json.RawMessage `json:"session_id"`
			Output  *string         `json:"output"`
		}
		if json.Unmarshal(data, &v) != nil || v.Chunk == "" || v.Output == nil {
			return
		}
		running := v.Exit == nil && len(v.Session) > 0 && string(v.Session) != "null"
		if v.Exit == nil && !running {
			return
		}
		h := sha256.Sum256([]byte(*v.Output))
		found = append(found, ProcessResult{Block: block, ExitCode: v.Exit, Running: running, OutputHash: hex.EncodeToString(h[:])})
	}
	blocks := outputBlocks(rec)
	if len(blocks) > 0 {
		for i, b := range blocks {
			parse([]byte(b), i+1)
		}
	} else {
		data := raw["output"]
		var text string
		if json.Unmarshal(data, &text) == nil {
			data = []byte(text)
		}
		parse(data, 0)
	}
	return found, true
}
func observations(calls []Evidence) []Observation {
	out := []Observation{}
	hiddenIDs := []string{}
	hiddenLines := []int{}
	failed := 0
	known, unknown, pending := 0, 0, 0
	// Exact outer input and exact failed output only. No assumption about an
	// unchanged environment; intermediate actions remain evidence for the reader.
	type hit struct {
		index int
		line  int
		id    string
	}
	repeats := map[string][]hit{}
	for i, e := range calls {
		switch e.Outcome {
		case "success", "failure":
			known++
		case "pending":
			pending++
		default:
			unknown++
		}
		bad := e.Outcome == "failure"
		hidden := 0
		signatures := map[string]bool{}
		for _, r := range e.ProcessResults {
			if r.ExitCode != nil && *r.ExitCode != 0 {
				hidden++
				bad = true
				signatures[fmt.Sprintf("%d:%s", *r.ExitCode, r.OutputHash)] = true
			}
		}
		if hidden > 0 && e.Outcome == "success" && (e.Tool == "exec" || e.Tool == "functions.exec") {
			failed += hidden
			hiddenIDs = append(hiddenIDs, e.CallID)
			if e.EndLine != nil {
				hiddenLines = append(hiddenLines, *e.EndLine)
			}
		}
		if !bad || e.Signals == nil || e.Signals.Fingerprint == "" || e.EndLine == nil {
			continue
		}
		// Without an output signature, a failed call is not called the same error.
		for sig := range signatures {
			key := e.Tool + ":" + e.Signals.Fingerprint + ":" + sig
			repeats[key] = append(repeats[key], hit{i, *e.EndLine, e.CallID})
		}
	}
	keys := []string{}
	for k, hits := range repeats {
		if len(hits) > 1 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		hits := repeats[k]
		first, last := hits[0], hits[len(hits)-1]
		between := []string{}
		for _, e := range calls[first.index+1 : last.index] {
			between = append(between, e.Tool)
		}
		ids, lines := []string{}, []int{}
		for _, h := range hits {
			ids = append(ids, h.id)
			lines = append(lines, h.line)
		}
		actions := "нет других вызовов"
		if len(between) > 0 {
			actions = strings.Join(between, ", ")
		}
		out = append(out, Observation{Kind: "repeated_failure", Fact: fmt.Sprintf("%d раза: одинаковый вход и одинаковый неуспешный результат процесса. Между первым и последним: %s.", len(hits), actions), Action: "Проверить, что изменилось между попытками. Повтор не доказывает бесполезность и не устанавливает причину ошибки.", CallIDs: ids, Lines: lines})
		break
	}
	if failed > 0 {
		out = append(out, Observation{Kind: "hidden_process_failure", Fact: fmt.Sprintf("В %d успешно завершённых exec напечатано %d результатов процессов с ненулевым exit_code.", len(hiddenIDs), failed), Action: "Проверять exit_code вложенных команд: успешное завершение exec не означает, что проверки или задача выполнены успешно.", CallIDs: hiddenIDs, Lines: hiddenLines})
	}
	if len(out) < 2 && (unknown > 0 || pending > 0) {
		ids := []string{}
		lines := []int{}
		for _, e := range calls {
			if e.Outcome == "unknown" || e.Outcome == "pending" {
				ids = append(ids, e.CallID)
				lines = append(lines, e.StartLine)
			}
		}
		out = append(out, Observation{Kind: "insufficient_evidence", Fact: fmt.Sprintf("Статус определён для %d/%d внешних вызовов; неизвестен для %d, без результата — %d.", known, len(calls), unknown, pending), Action: "Не считать отсутствие зарегистрированных ошибок доказательством успешной работы. Нужен полный исходный журнал.", CallIDs: ids, Lines: lines})
	}
	if len(out) == 0 {
		out = append(out, Observation{Kind: "no_findings", Fact: "Повторных неуспешных результатов с одинаковым входом и ошибок процессов внутри успешного exec не обнаружено.", Action: "Это результат по доступным событиям, не проверка правильности решения задачи.", CallIDs: []string{}, Lines: []int{}})
	}
	return out
}
