package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	a2a "github.com/mnhkahn/xiaoli/server/internal/a2a"
)

type newsScorerFunc func(context.Context, geekNewsItem) (geekNewsScores, error)

// Opt in explicitly: this test makes a billed request using synthetic public text.
func TestJevNewsScorerLive(t *testing.T) {
	if os.Getenv("XIAOLI_TEST_JEV") != "1" {
		t.Skip("set XIAOLI_TEST_JEV=1 to test the live Decisions API")
	}
	scorer := newJevNewsScorer("typesafe/jev-1.13", os.Getenv("OPENROUTER_API_KEY"))
	scores, err := scorer.Score(context.Background(), geekNewsItem{
		SourceTitle:       "Example: open-source JSON parser adds streaming support",
		sourceDescription: "Synthetic test news: An open-source JSON parser adds incremental parsing for large files. The release provides Go examples and a migration guide. A reproducible benchmark reports peak memory falling from 800 MB to 40 MB on a 1 GB input. The change is available under the MIT license.",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("importance=%.3f usefulness=%.3f information=%.3f total=%.3f", scores.Importance, scores.Usefulness, scores.Information, scores.total())
}

func (f newsScorerFunc) Score(ctx context.Context, item geekNewsItem) (geekNewsScores, error) {
	return f(ctx, item)
}

func TestJevNewsScorer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("incorrect method or authentication")
		}
		var request struct {
			Model     string                      `json:"model"`
			State     map[string]string           `json:"state"`
			Questions map[string]jevScoreQuestion `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "typesafe/custom-version" || !reflect.DeepEqual(request.State, map[string]string{"title": "original title", "description": "original description"}) {
			t.Errorf("unexpected input: %+v", request)
		}
		if len(request.Questions) != 3 {
			t.Error("expected three dimensions")
		}
		for _, q := range request.Questions {
			if q.Type != "score" || len(q.Criteria) != 5 {
				t.Errorf("invalid question: %+v", q)
			}
		}
		fmt.Fprint(w, `{"answers":{"importance":{"type":"score","score":3.5},"usefulness":{"type":"score","score":2},"information":{"type":"score","score":4}}}`)
	}))
	defer server.Close()
	scorer := newJevNewsScorer("typesafe/custom-version", "test-key")
	scorer.endpoint = server.URL
	got, err := scorer.Score(context.Background(), geekNewsItem{SourceTitle: "original title", sourceDescription: "original description", Title: "translated", Description: "rewritten", CreateTime: 123})
	if err != nil || got != (geekNewsScores{3.5, 2, 4}) {
		t.Fatalf("scores=%+v err=%v", got, err)
	}
	if got.total() != 3.1 {
		t.Fatalf("total=%v", got.total())
	}
}

func TestJevRejectsInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"http", `{}`, 429},
		{"malformed", `{`, 200},
		{"missing", `{"answers":{}}`, 200},
		{"null", `{"answers":{"importance":{"type":"score","score":null}}}`, 200},
		{"out_of_range", `{"answers":{"importance":{"type":"score","score":5},"usefulness":{"type":"score","score":2},"information":{"type":"score","score":1}}}`, 200},
		{"wrong_type", `{"answers":{"importance":{"type":"choice","score":2},"usefulness":{"type":"score","score":2},"information":{"type":"score","score":1}}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			scorer := newJevNewsScorer("typesafe/custom-version", "test-key")
			scorer.endpoint = server.URL
			if _, err := scorer.Score(context.Background(), geekNewsItem{}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	if _, err := newJevNewsScorer("typesafe/jev-1.13", "").Score(context.Background(), geekNewsItem{}); err == nil {
		t.Fatal("expected missing key error")
	}
}

func TestNewsRankingWeightsStableTiesAndFallback(t *testing.T) {
	items := []geekNewsItem{{Title: "info", CreateTime: 999}, {Title: "important"}, {Title: "useful"}, {Title: "tie"}}
	original := append([]geekNewsItem(nil), items...)
	p := &a2aPipeline{newsScorer: newsScorerFunc(func(_ context.Context, item geekNewsItem) (geekNewsScores, error) {
		switch item.Title {
		case "important":
			return geekNewsScores{4, 0, 0}, nil
		case "useful":
			return geekNewsScores{0, 4, 0}, nil
		default:
			return geekNewsScores{0, 0, 4}, nil
		}
	})}
	got := p.rankGeekNewsItems(context.Background(), a2a.ConversationTurn{}, "news", items)
	want := []geekNewsItem{items[1], items[2], items[0], items[3]}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(items, original) {
		t.Fatalf("got=%+v original=%+v", got, items)
	}
	p.newsScorer = newsScorerFunc(func(_ context.Context, item geekNewsItem) (geekNewsScores, error) {
		if item.Title == "tie" {
			return geekNewsScores{}, errors.New("failed")
		}
		return geekNewsScores{4, 4, 4}, nil
	})
	if got := p.rankGeekNewsItems(context.Background(), a2a.ConversationTurn{}, "news", items); !reflect.DeepEqual(got, items) {
		t.Fatal("failure must preserve entire group order")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := p.rankGeekNewsItems(ctx, a2a.ConversationTurn{}, "news", items); !reflect.DeepEqual(got, items) {
		t.Fatal("cancellation must preserve order")
	}
}
