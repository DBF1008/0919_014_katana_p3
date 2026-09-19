package crawler

import (
	"context"

	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/pipeline"
)

// GraphWriterStage persists the page state produced by discovery into the
// crawl graph. It only runs for in-scope pages with a valid state.
type graphWriterStage struct {
	c *Crawler
}

func (graphWriterStage) Name() pipeline.StageName { return StageGraphWriter }

func (s graphWriterStage) Process(_ context.Context, item *crawlItem) error {
	if item.pageState == nil {
		return nil
	}
	return s.c.crawlGraph.AddPageState(*item.pageState)
}
