package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"agentlens/internal/advice"
	"agentlens/internal/lens"
	reportview "agentlens/internal/report"
)

const help = `agentlens — Codex tool failure evidence (offline by default)
Usage:
  agentlens analyze --input session.jsonl [--json] [--details]
  agentlens analyze --input sessions/ [--json] [--details]
  agentlens prepare --input rollout.jsonl --output session.jsonl
Input: Codex rollout JSONL: session_meta; response_item with function_call /
custom_tool_call and corresponding *_output, linked by call_id; timestamps
in RFC3339; event_msg task_started/task_complete. Not codex exec --json.
Directories: immediate *.jsonl files, sorted; no recursion or symlinks.
--details: show per-call IDs, timestamps and evidence lines.
--ai: one model opinion; OPENAI_API_KEY, OPENAI_MODEL, optional OPENAI_BASE_URL.
--ai-output: new file for exact AI context and response (default timestamped).
Default: external Codex/MCP calls and evidence-based observations.
OK/ERR/?/WAIT: success/failure/unknown/pending. References in exec are not calls.
p50: median; p95: 95th percentile. n/a: unavailable.
Total tokens = input + output; cached/reasoning tokens are subsets.
Durations are call-to-result latency, not CPU time. Missing timing stays null.
Exit codes: 0 analyzed (warnings allowed), 1 invalid/unreadable input, 2 usage.
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, out, errout io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, help)
		return 0
	}
	cmd := args[0]
	if cmd != "analyze" && cmd != "prepare" {
		fmt.Fprintln(errout, "unknown command:", cmd)
		return 2
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(errout)
	fs.Usage = func() { fmt.Fprint(out, help) }
	input := fs.String("input", "", "file or directory")
	aiEnabled := fs.Bool("ai", false, "one optional model opinion")
	aiOutput := fs.String("ai-output", "", "AI context and response file (new file)")
	details := fs.Bool("details", false, "show per-call evidence")
	asJSON := fs.Bool("json", false, "JSON report")
	output := fs.String("output", "", "prepared output path")
	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *input == "" || fs.NArg() != 0 {
		fmt.Fprintln(errout, "--input is required; positional arguments are not accepted")
		return 2
	}
	if cmd == "prepare" {
		if *output == "" || *asJSON || *details || *aiEnabled || *aiOutput != "" {
			fmt.Fprintln(errout, "prepare requires --output and does not accept --json/--details")
			return 2
		}
		if err := prepare(*input, *output); err != nil {
			fmt.Fprintln(errout, err)
			return 1
		}
		return 0
	}
	if *output != "" {
		fmt.Fprintln(errout, "analyze does not accept --output")
		return 2
	}
	if *aiOutput != "" && !*aiEnabled {
		fmt.Fprintln(errout, "--ai-output requires --ai")
		return 2
	}
	report := analyze(*input)
	if *aiEnabled && len(report.Errors) == 0 && report.SessionCount > 0 {
		if *aiOutput == "" {
			*aiOutput = "agentlens-ai-" + time.Now().UTC().Format("20060102T150405.000000000") + ".json"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		report.AI = advice.Review(ctx, advice.FromEnvironment(), report, *aiOutput, nil)
		cancel()
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(errout, err)
			return 1
		}
	} else {
		reportview.Print(out, report, *details)
	}
	for _, e := range report.Errors {
		fmt.Fprintf(errout, "%s: %s\n", e.File, e.Error)
	}
	if report.AI != nil && report.AI.Error != "" {
		fmt.Fprintln(errout, report.AI.Error)
		return 1
	}
	if len(report.Errors) > 0 {
		return 1
	}
	return 0
}
func analyze(input string) lens.Report {
	r := lens.Report{Schema: "agentlens.session.v2", Complete: true, Sessions: []lens.Session{}, Groups: []lens.Group{}, Errors: []lens.FileError{}}
	fail := func(path string, err error) {
		r.Errors = append(r.Errors, lens.FileError{File: path, Error: err.Error()})
		r.Complete = false
	}
	info, err := os.Lstat(input)
	if err != nil {
		fail(input, err)
		return r
	}
	paths := []string{input}
	if info.IsDir() {
		paths = nil
		entries, e := os.ReadDir(input)
		if e != nil {
			fail(input, e)
			return r
		}
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) == ".jsonl" {
				paths = append(paths, filepath.Join(input, entry.Name()))
			}
		}
		if len(paths) == 0 {
			fail(input, fmt.Errorf("no .jsonl session files"))
			return r
		}
	}
	all := []lens.Evidence{}
	ids := map[string]bool{}
	for _, path := range paths {
		inf, e := os.Lstat(path)
		if e != nil {
			fail(path, e)
			continue
		}
		if !inf.Mode().IsRegular() {
			fail(path, fmt.Errorf("not a regular file (symlinks are not followed)"))
			continue
		}
		f, e := os.Open(path)
		if e != nil {
			fail(path, e)
			continue
		}
		s, e := lens.Analyze(f, path)
		f.Close()
		if e != nil {
			fail(path, e)
			continue
		}
		if ids[s.ID] {
			fail(path, fmt.Errorf("duplicate session_id %s", s.ID))
			continue
		}
		ids[s.ID] = true
		r.Sessions = append(r.Sessions, s)
		all = append(all, s.Calls...)
		if len(s.Warnings) > 0 {
			r.Complete = false
		}
	}
	r.SessionCount = len(r.Sessions)
	r.Groups = lens.Groups(all)
	return r
}
func prepare(input, output string) error {
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	defer f.Close()
	// Validate first; avoid exporting a superficially valid non-rollout file.
	if _, err = lens.Analyze(f, input); err != nil {
		return err
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(output), ".agentlens-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	err = lens.Prepare(f, tmp)
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Do not silently replace an existing dataset.
	if err = os.Link(tmp.Name(), output); err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	return nil
}
