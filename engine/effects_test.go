package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestGiteaResolveTrackedPRSetBatchesList(t *testing.T) {
	pullRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/test/up/pulls" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		pullRequests++
		if got := r.URL.Query().Get("state"); got != "all" {
			t.Fatalf("state query = %q, want all", got)
		}
		if got := r.URL.Query().Get("limit"); got != "1000" {
			t.Fatalf("limit query = %q, want 1000", got)
		}
		data := []map[string]any{
			{"number": 1, "state": "open", "head": map[string]string{"sha": "aaa"}},
			{"number": 2, "state": "closed", "head": map[string]string{"sha": "bbb"}},
			{"number": 3, "state": "open", "head": map[string]string{"sha": "ccc"}},
		}
		if err := json.NewEncoder(w).Encode(data); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	client := &giteaForgeClient{
		rest: &giteaREST{base: server.URL},
		repo: "test/up",
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	heads, err := client.ResolvePRSet(&Config{hasTrack: true, track: []int{3, 1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	if pullRequests != 1 {
		t.Fatalf("pull list requests = %d, want 1", pullRequests)
	}
	want := map[string]string{"1": "aaa", "3": "ccc"}
	if !reflect.DeepEqual(heads, want) {
		t.Fatalf("ResolvePRSet() = %#v, want %#v", heads, want)
	}
}
