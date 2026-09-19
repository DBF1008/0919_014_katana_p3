package crawler

import (
	"context"
	"log/slog"

	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/pipeline"
)

// ActionProcessorStage is the first pipeline stage. It fingerprints the
// browser page currently held by the pooled tab so downstream stages know
// whether origin restoration is required.
type actionProcessorStage struct {
	c *Crawler
}

func (actionProcessorStage) Name() pipeline.StageName { return StageActionProcessor }

func (s actionProcessorStage) Process(_ context.Context, item *crawlItem) error {
	currentPageHash, _, err := getPageHash(item.page)
	if err != nil {
		return err
	}
	item.currentHash = currentPageHash

	s.c.logger.Debug("Processing action - current state",
		slog.String("current_page_hash", currentPageHash),
		slog.String("action_origin_id", item.action.OriginID),
		slog.String("action", item.action.String()),
	)
	return nil
}
