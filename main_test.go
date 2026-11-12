package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentlens/internal/lens"
)

func TestPartialDirectory(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "good.jsonl"), []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"s\"}}\n"), 0600)
	os.WriteFile(filepath.Join(dir, "bad.jsonl"), []byte("broken"), 0600)
	var out, errout bytes.Buffer
	code := run([]string{"analyze", "--input", dir, "--json"}, &out, &errout)
	var r lens.Report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if code != 1 || r.SessionCount != 1 || r.Complete || len(r.Errors) != 1 || !strings.Contains(errout.String(), "bad.jsonl") {
		t.Fatalf("%d %+v %s", code, r, errout.String())
	}
}
func TestCLI(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{{[]string{"analyze", "--help"}, 0}, {[]string{"analyze"}, 2}, {[]string{"wat"}, 2}, {[]string{"analyze", "--input", "/nonexistent", "--json"}, 1}} {
		var o, e bytes.Buffer
		if got := run(tc.args, &o, &e); got != tc.code {
			t.Fatalf("%v: %d", tc.args, got)
		}
	}
}
func TestDuplicateSessionsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	data := []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"s\"}}\n")
	for _, name := range []string{"a.jsonl", "b.jsonl"} {
		os.WriteFile(filepath.Join(dir, name), data, 0600)
	}
	os.Symlink("a.jsonl", filepath.Join(dir, "c.jsonl"))
	r := analyze(dir)
	if r.SessionCount != 1 || len(r.Errors) != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestReadableSummaryAndDetails(t *testing.T) {
	for _, details := range []bool{false, true} {
		var out, errout bytes.Buffer
		args := []string{"analyze", "--input", "testdata/real/library-review.jsonl"}
		if details {
			args = append(args, "--details")
		}
		if code := run(args, &out, &errout); code != 0 {
			t.Fatal(errout.String())
		}
		s := out.String()
		if !strings.Contains(s, "НАБЛЮДЕНИЯ") || strings.Contains(s, "call_") != details {
			t.Fatal(s)
		}
	}
}
