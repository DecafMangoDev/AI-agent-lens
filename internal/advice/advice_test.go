package advice

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentlens/internal/lens"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOneRequestAndArtifact(t *testing.T) {
	for _, code := range []int{200, 429, 302} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret-key" {
					t.Fatal("bad request")
				}
				b, _ := io.ReadAll(r.Body)
				if strings.Contains(string(b), "secret-key") {
					t.Fatal("key in body")
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Location": []string{"https://elsewhere.invalid"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"Гипотеза: проверить коды возврата."}}]}`))}, nil
			})}
			path := filepath.Join(t.TempDir(), "ai.json")
			got := Review(context.Background(), Config{Key: "secret-key", BaseURL: "https://example.invalid/v1", Model: "test"}, lens.Report{}, path, client)
			if calls != 1 || (code == 200) != (got.Error == "") {
				t.Fatalf("%d %+v", calls, got)
			}
			b, err := os.ReadFile(path)
			if err != nil || !json.Valid(b) || strings.Contains(string(b), "secret-key") || !strings.Contains(string(b), "messages") {
				t.Fatalf("artifact: %s %v", b, err)
			}
		})
	}
}
func TestNoRequestForBadConfigOrExistingArtifact(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { calls++; t.Fatal("unexpected request"); return nil, nil })}
	path := filepath.Join(t.TempDir(), "ai.json")
	os.WriteFile(path, []byte("existing"), 0600)
	for _, cfg := range []Config{{}, {Key: "k", Model: "m", BaseURL: "https://example.invalid"}} {
		if got := Review(context.Background(), cfg, lens.Report{}, path, client); got.Error == "" {
			t.Fatal(got)
		}
	}
	if calls != 0 {
		t.Fatal(calls)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "existing" {
		t.Fatal("overwrote artifact")
	}
}
func TestBoundedContext(t *testing.T) {
	r := lens.Report{SessionCount: 20}
	for i := 0; i < 20; i++ {
		r.Sessions = append(r.Sessions, lens.Session{ID: strings.Repeat("я", 10000), Observations: []lens.Observation{{Fact: strings.Repeat("я", 10000), Action: strings.Repeat("я", 10000)}}})
	}
	s := Context(r)
	if len(s) > 24576 || !json.Valid([]byte(s)) {
		t.Fatal("invalid or oversized context")
	}
}
