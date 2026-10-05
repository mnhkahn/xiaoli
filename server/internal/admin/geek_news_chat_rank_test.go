package admin

import (
	"context"
	"testing"

	a2a "github.com/mnhkahn/xiaoli/server/internal/a2a"
	"github.com/stretchr/testify/require"
)

func TestNewsRankingModeSelection(t *testing.T) {
	items := []geekNewsItem{{Title: "first"}, {Title: "second"}}
	for _, mode := range []string{"", "chat", "jev", "typo"} {
		t.Run(mode, func(t *testing.T) {
			agent := &fakeA2AAgent{structuredArguments: `{"ids":["n1","n0"]}`}
			p := newA2APipeline(agent, map[string]A2AProfileConfig{"geek-news": {RankingMode: mode, Model: "configured-chat"}})
			p.newsScorer = newsScorerFunc(func(_ context.Context, item geekNewsItem) (geekNewsScores, error) {
				if mode != "jev" {
					t.Error("non-Jev mode called billed decision API")
				}
				if item.Title == "second" {
					return geekNewsScores{4, 4, 4}, nil
				}
				return geekNewsScores{}, nil
			})
			profile := a2aPromptProfileSpec{}
			p.applyProfileOverrides(&profile, "geek-news")
			got := p.sortGeekNewsItems(context.Background(), a2a.ConversationTurn{}, profile, "news", items)
			if mode == "typo" {
				require.Equal(t, items, got)
			} else {
				require.Equal(t, []geekNewsItem{items[1], items[0]}, got)
			}
			if mode == "" || mode == "chat" {
				require.Equal(t, 1, agent.profileCalls)
				require.Equal(t, "configured-chat", agent.lastProfile.Model)
				require.Contains(t, agent.lastProfile.SystemPrompt, "不考虑时效性")
			} else {
				require.Zero(t, agent.profileCalls)
			}
		})
	}
}

func TestChatNewsRankingFallbackAndPreservation(t *testing.T) {
	items := []geekNewsItem{{Title: "first"}, {Title: "second"}, {Title: "third"}}
	for _, response := range []string{`not json`, `{"ids":[]}`, ``} {
		p := newA2APipeline(&fakeA2AAgent{structuredArguments: response}, nil)
		require.Equal(t, items, p.sortGeekNewsItems(context.Background(), a2a.ConversationTurn{}, a2aPromptProfileSpec{}, "news", items))
	}
	require.Equal(t, []geekNewsItem{items[1], items[0], items[2]}, applyGeekNewsOrder(items, []string{"n1", "n1", "n99", "n0junk"}))
}

func TestShippedNewsRankingConfigUsesChat(t *testing.T) {
	settings, path := loadSettings([]string{"../../settings.json"})
	require.NotEmpty(t, path)
	require.Equal(t, "chat", settings.A2A.Profiles["geek-news"].RankingMode)
}
