package lens

import (
	"encoding/json"
	"regexp"
	"strconv"
)

// Outcomes concern the recorded tool, not correctness of the agent's task.
var exitLine = regexp.MustCompile(`(?m)^Process exited with code (-?[0-9]+)\r?$`)

func resultOutcome(rec Record, p Payload) string {
	switch p.Outcome {
	case "success", "failure", "unknown":
		return p.Outcome
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(rec.Payload, &raw) != nil {
		return "unknown"
	}
	output := raw["output"]
	var text string
	if json.Unmarshal(output, &text) == nil {
		output = []byte(text)
		if m := exitLine.FindStringSubmatch(text); m != nil {
			code, e := strconv.Atoi(m[1])
			if e == nil {
				if code == 0 {
					return "success"
				}
				return "failure"
			}
		}
	}
	var obj struct {
		IsError  *bool `json:"isError"`
		ExitCode *int  `json:"exit_code"`
		TimedOut bool  `json:"timed_out"`
	}
	if json.Unmarshal(output, &obj) != nil {
		return "unknown"
	}
	if obj.TimedOut || (obj.IsError != nil && *obj.IsError) || (obj.ExitCode != nil && *obj.ExitCode != 0) {
		return "failure"
	}
	if obj.IsError != nil || obj.ExitCode != nil {
		return "success"
	}
	return "unknown"
}
