package crawler

import (
	"io"
	"log/slog"
	"testing"

	"github.com/adrianbrad/queue"
	"github.com/projectdiscovery/katana/pkg/engine/headless/graph"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCollectCrawler() *Crawler {
	return &Crawler{
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		crawlQueue:    queue.NewLinked([]*types.Action{}),
		uniqueActions: make(map[string]struct{}),
	}
}

func navClick(href, text string) *types.Action {
	return &types.Action{
		Type: types.ActionTypeLeftClick,
		Element: &types.HTMLElement{
			TagName:     "a",
			Attributes:  map[string]string{"href": href},
			TextContent: text,
		},
	}
}

func TestCollectNavigations_EnqueuesAndStampsOrigin(t *testing.T) {
	c := newCollectCrawler()
	state := &types.PageState{UniqueID: "state-1"}

	navs := []*types.Action{navClick("/a", "A"), navClick("/b", "B")}
	c.collectNavigations(state, navs)

	require.Equal(t, 2, c.crawlQueue.Size())
	first, err := c.crawlQueue.Get()
	require.NoError(t, err)
	assert.Equal(t, "state-1", first.OriginID)
}

func TestCollectNavigations_DeduplicatesAcrossPages(t *testing.T) {
	c := newCollectCrawler()

	c.collectNavigations(&types.PageState{UniqueID: "s1"}, []*types.Action{navClick("/a", "A")})
	// Same hash on another page: dropped.
	c.collectNavigations(&types.PageState{UniqueID: "s2"}, []*types.Action{navClick("/a", "A")})

	assert.Equal(t, 1, c.crawlQueue.Size())
}

func TestCollectNavigations_SkipsLogoutLinks(t *testing.T) {
	c := newCollectCrawler()

	navs := []*types.Action{
		navClick("/logout", "Log out"),
		navClick("/home", "Home"),
	}
	c.collectNavigations(&types.PageState{UniqueID: "s1"}, navs)

	assert.Equal(t, 1, c.crawlQueue.Size())
	only, err := c.crawlQueue.Get()
	require.NoError(t, err)
	assert.Equal(t, "/home", only.Element.Attributes["href"])
}

func TestGraphWriterStage_NoPageStateIsNoop(t *testing.T) {
	c := &Crawler{crawlGraph: graph.NewCrawlGraph()}
	stage := graphWriterStage{c: c}

	require.NoError(t, stage.Process(nil, &crawlItem{}))
	assert.Empty(t, c.crawlGraph.GetVertices())
}
