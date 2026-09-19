package crawler

import (
	"context"

	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/pipeline"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
)

// Stage names exported for lifecycle-hook consumers. The order below is the
// canonical pipeline order.
const (
	// StageActionProcessor fingerprints the current page and prepares the
	// action for execution.
	StageActionProcessor pipeline.StageName = "action-processor"
	// StageNavigator restores the action's origin state and dispatches the
	// action (load-url / click / form-fill).
	StageNavigator pipeline.StageName = "navigator"
	// StageCaptchaHandler detects captcha pages and attempts to solve them.
	StageCaptchaHandler pipeline.StageName = "captcha-handler"
	// StageAuthHandler performs automatic login form submission.
	StageAuthHandler pipeline.StageName = "auth-handler"
	// StageDiscoveryCollector collects, de-duplicates and enqueues the
	// navigations discovered on the current page.
	StageDiscoveryCollector pipeline.StageName = "discovery-collector"
	// StageGraphWriter persists the post-action page state in the crawl graph.
	StageGraphWriter pipeline.StageName = "graph-writer"
)

// crawlItem is the envelope carried through the crawler pipeline.
type crawlItem struct {
	// skip short-circuits downstream stages (see pipeline.Skippable).
	skip        bool
	action      *types.Action
	page        *browser.BrowserPage
	currentHash string
	pageState   *types.PageState
	navigations []*types.Action
}

// Skipped implements pipeline.Skippable.
func (i *crawlItem) Skipped() bool { return i.skip }

// SkipRemaining implements pipeline.Skippable.
func (i *crawlItem) SkipRemaining() { i.skip = true }

func newCrawlItem(ctx context.Context, action *types.Action, page *browser.BrowserPage) *crawlItem {
	return &crawlItem{action: action, page: page}
}

// buildStages returns the ordered stage list for a crawl.
func (c *Crawler) buildStages() []pipeline.Stage[*crawlItem] {
	return []pipeline.Stage[*crawlItem]{
		actionProcessorStage{c: c},
		navigatorStage{c: c},
		captchaHandlerStage{c: c},
		authHandlerStage{c: c},
		discoveryCollectorStage{c: c},
		graphWriterStage{c: c},
	}
}

// StageName is re-exported so callers of the crawler package (and the
// headless facade) can reference stage identifiers without importing the
// pipeline sub-package directly.
type StageName = pipeline.StageName
