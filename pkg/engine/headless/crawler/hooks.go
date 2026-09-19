package crawler

import (
	"context"

	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/pipeline"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
)

// Hooks bundles optional lifecycle callbacks invoked by the headless crawler.
// All fields are optional; nil callbacks are skipped.
//
// Callbacks run synchronously and block progress for their duration — they
// should return quickly. A non-nil error returned from a "before" callback
// aborts the surrounding crawl step; an "after" callback's error replaces the
// (otherwise nil) step error.
//
// The callback model is aligned with the pipeline architecture:
//
//   - BeforeCrawl / AfterCrawl bracket an entire Crawl invocation.
//   - BeforeStage / AfterStage bracket each of the six pipeline stages
//     (ActionProcessor, Navigator, CaptchaHandler, AuthHandler,
//     DiscoveryCollector, GraphWriter) for every action.
//   - BeforeAction / AfterAction bracket the concrete action dispatch inside
//     the Navigator stage (load-url / click / form-fill) and state-restoration
//     replays performed by the shortest-path strategy.
//   - BeforeNavigateBack brackets each browser-history back step.
//
// Skipped stages (captcha pages and out-of-scope pages) do not invoke stage
// hooks. A Hooks value is consulted on every action; callers may safely mutate
// the fields between Crawl invocations but should not mutate them while a
// Crawl is in flight.
//
// The supplied *browser.BrowserPage embeds *rod.Page (page.Page) for callers
// that want to reach the raw rod API. Callbacks should treat the page as
// read-only — navigating, closing, or otherwise mutating it from inside a
// callback races with the crawler.
type Hooks struct {
	// BeforeCrawl is invoked once after a target URL has been accepted and
	// the crawl graph has been initialized, before any action runs. A
	// non-nil error aborts the crawl.
	BeforeCrawl func(ctx context.Context, targetURL string) error
	// AfterCrawl is invoked once when Crawl finishes, whether it completed,
	// was cancelled or failed; the crawl's terminal error (if any) is passed
	// in. Its own error is returned to the Crawl caller when the crawl itself
	// succeeded.
	AfterCrawl func(ctx context.Context, targetURL string, crawlErr error) error

	// BeforeStage is invoked before every non-skipped pipeline stage. A
	// non-nil error aborts processing of the action for that stage.
	BeforeStage func(page *browser.BrowserPage, action *types.Action, stage pipeline.StageName) error
	// AfterStage is invoked after a stage completes successfully. It is not
	// called when the stage was skipped or returned an error. A non-nil error
	// aborts further processing of the action.
	AfterStage func(page *browser.BrowserPage, action *types.Action, stage pipeline.StageName) error

	// BeforeAction is invoked just before each action is dispatched, including
	// actions that will subsequently fail. A non-nil error aborts the action.
	BeforeAction func(page *browser.BrowserPage, action *types.Action) error
	// AfterAction is invoked only after an action completes successfully.
	// It is not called when the action returns an error. A non-nil error from
	// AfterAction is returned in place of the (otherwise nil) action error.
	AfterAction func(page *browser.BrowserPage, action *types.Action) error
	// BeforeNavigateBack is invoked once per browser-history back step during
	// state restoration, immediately before page.NavigateBack(). A non-nil
	// error aborts the navigation.
	BeforeNavigateBack func(page *browser.BrowserPage) error
}

// runWithCrawlHooks invokes hooks.BeforeCrawl, then fn, and always invokes
// hooks.AfterCrawl with the crawl's terminal error. The returned error is the
// first non-nil error in this order: BeforeCrawl error, fn error, AfterCrawl
// error. AfterCrawl still runs when BeforeCrawl fails, receiving the
// BeforeCrawl error.
func runWithCrawlHooks(hooks Hooks, ctx context.Context, targetURL string, fn func() error) (err error) {
	beforeErr := runBeforeCrawlHook(hooks, ctx, targetURL)
	defer func() {
		if cerr := runAfterCrawlHook(hooks, ctx, targetURL, firstError(beforeErr, err)); cerr != nil && err == nil {
			err = cerr
		}
	}()
	if beforeErr != nil {
		return beforeErr
	}
	return fn()
}

func runBeforeCrawlHook(hooks Hooks, ctx context.Context, targetURL string) error {
	if cb := hooks.BeforeCrawl; cb != nil {
		return cb(ctx, targetURL)
	}
	return nil
}

func runAfterCrawlHook(hooks Hooks, ctx context.Context, targetURL string, crawlErr error) error {
	if cb := hooks.AfterCrawl; cb != nil {
		return cb(ctx, targetURL, crawlErr)
	}
	return nil
}

// runWithStageHooks invokes hooks.BeforeStage, then fn, then on success
// hooks.AfterStage, returning the first non-nil error encountered.
func runWithStageHooks(hooks Hooks, page *browser.BrowserPage, action *types.Action, stage pipeline.StageName, fn func() error) (err error) {
	if cb := hooks.BeforeStage; cb != nil {
		if err := cb(page, action, stage); err != nil {
			return err
		}
	}
	defer func() {
		if err != nil {
			return
		}
		if cb := hooks.AfterStage; cb != nil {
			if cerr := cb(page, action, stage); cerr != nil {
				err = cerr
			}
		}
	}()
	return fn()
}

// runWithActionHooks invokes hooks.BeforeAction, then fn, then on success
// hooks.AfterAction, returning the first non-nil error encountered. The
// semantics are:
//
//   - BeforeAction error → fn and AfterAction are skipped; error is returned.
//   - fn error           → AfterAction is skipped; fn error is returned.
//   - fn nil, AfterAction error → AfterAction error is returned.
//
// runWithActionHooks must remain side-effect-free beyond the supplied hooks
// so its semantics can be verified in isolation.
func runWithActionHooks(hooks Hooks, page *browser.BrowserPage, action *types.Action, fn func() error) (err error) {
	if cb := hooks.BeforeAction; cb != nil {
		if err := cb(page, action); err != nil {
			return err
		}
	}
	defer func() {
		if err != nil {
			return
		}
		if cb := hooks.AfterAction; cb != nil {
			if cerr := cb(page, action); cerr != nil {
				err = cerr
			}
		}
	}()
	return fn()
}

// runWithNavigateBackHook invokes hooks.BeforeNavigateBack and then fn,
// short-circuiting on a non-nil error from either.
func runWithNavigateBackHook(hooks Hooks, page *browser.BrowserPage, fn func() error) error {
	if cb := hooks.BeforeNavigateBack; cb != nil {
		if err := cb(page); err != nil {
			return err
		}
	}
	return fn()
}

// firstError returns a if non-nil, otherwise b.
func firstError(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
