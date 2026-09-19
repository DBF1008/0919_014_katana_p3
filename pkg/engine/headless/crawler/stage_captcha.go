package crawler

import (
	"context"

	"github.com/projectdiscovery/gologger"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/pipeline"
)

// CaptchaHandlerStage inspects the post-action page for a captcha and attempts
// to solve it. On a handled captcha the remaining stages (auth, discovery,
// graph write) are skipped: the discovered links/forms belong to the captcha
// widget rather than the real page.
type captchaHandlerStage struct {
	c *Crawler
}

func (captchaHandlerStage) Name() pipeline.StageName { return StageCaptchaHandler }

func (s captchaHandlerStage) Process(ctx context.Context, item *crawlItem) error {
	if s.c.options.CaptchaHandler == nil {
		return nil
	}

	page := item.page
	html, htmlErr := page.HTML()
	if htmlErr != nil {
		// HTML unavailability must not abort the crawl; discovery simply runs
		// on whatever the page currently exposes.
		return nil
	}

	handled, solveErr := s.c.options.CaptchaHandler.HandleIfCaptcha(ctx, page.Page, html)
	if solveErr != nil {
		gologger.Warning().Msgf("captcha solving failed: %s", solveErr)
	}
	if handled && solveErr == nil {
		_ = page.WaitPageLoadHeurisitics()
	}
	if handled {
		// Skip navigation discovery on captcha pages — the discovered
		// links/forms belong to the captcha widget, not the real page.
		item.SkipRemaining()
	}
	return nil
}
