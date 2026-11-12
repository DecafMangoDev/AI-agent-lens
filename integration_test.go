package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRealData(t *testing.T) {
	r := analyze("testdata/real")
	if len(r.Errors) > 0 || r.SessionCount != 3 {
		t.Fatalf("%+v", r)
	}
	n := 0
	for _, s := range r.Sessions {
		n += len(s.Calls)
	}
	if n != 110 {
		t.Fatalf("calls: %d", n)
	}
}
func TestSyntheticSessions(t *testing.T) {
	var expected struct {
		Cases []struct {
			File  string   `json:"file"`
			Kinds []string `json:"expected_current_kinds"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("testdata/synthetic-observations/expected.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	for _, c := range expected.Cases {
		t.Run(c.File, func(t *testing.T) {
			r := analyze(filepath.Join("testdata/synthetic-observations/sessions", c.File))
			if len(r.Errors) > 0 || len(r.Sessions) != 1 {
				t.Fatalf("%+v", r.Errors)
			}
			kinds := []string{}
			for _, o := range r.Sessions[0].Observations {
				kinds = append(kinds, o.Kind)
			}
			if !reflect.DeepEqual(kinds, c.Kinds) {
				t.Fatalf("got %v; want %v", kinds, c.Kinds)
			}
		})
	}
}
