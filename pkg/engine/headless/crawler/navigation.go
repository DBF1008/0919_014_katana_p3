package crawler

import (
	"context"
	"log/slog"

	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
)

// NavigationContext bundles every dependency a NavigationStrategy may need to
// restore the browser to an action's origin state. Strategies receive it by
// pointer and must not mutate it.
type NavigationContext struct {
	// Ctx is the crawl-scoped context.
	Ctx context.Context
	// Crawler exposes the graph, diagnostics, logger and hooks. Strategies are
	// expected to stay within the navigation surface (graph traversal, page
	// interaction) and not enqueue new actions.
	Crawler *Crawler
	// Action is the queued action whose OriginID must be restored.
	Action *types.Action
	// Page is the pooled browser page to navigate.
	Page *browser.BrowserPage
	// Origin is the graph vertex recorded for Action.OriginID.
	Origin *types.PageState
	// CurrentHash is the page-state hash observed before restoration.
	CurrentHash string
}

// NavigationResult is the outcome of a single strategy attempt.
type NavigationResult struct {
	// PageHash is empty when the strategy does not apply. A non-empty hash
	// means the origin was reached (or a simhash-equivalent page was).
	PageHash string
}

// NavigationStrategy restores the browser page to the origin state of an
// action before the action itself is dispatched.
//
// Contract:
//
//   - Applicable, success: result.PageHash != "", err == nil.
//   - Not applicable: result.PageHash == "", err == nil; the chain tries the
//     next strategy.
//   - Failure: result.PageHash == "", err != nil. A failure on a non-final
//     strategy is logged and the chain falls through; a failure on the final
//     strategy is returned to the caller.
//
// New strategies are added by registering a NavigationStrategy; the core
// chain logic never changes.
type NavigationStrategy interface {
	// Name identifies the strategy in logs.
	Name() string
	// Restore attempts to navigate the page to nctx.Action.OriginID.
	Restore(nctx *NavigationContext) (NavigationResult, error)
}

// NavigationStrategyChain is an ordered list of strategies tried by priority.
// The first applicable, successful strategy wins.
type NavigationStrategyChain struct {
	logger     *slog.Logger
	strategies []NavigationStrategy
}

// NewNavigationStrategyChain assembles a chain from the given strategies in
// priority order (highest priority first).
func NewNavigationStrategyChain(logger *slog.Logger, strategies ...NavigationStrategy) *NavigationStrategyChain {
	return &NavigationStrategyChain{logger: logger, strategies: strategies}
}

// Strategies returns the registered strategies in priority order. It is
// primarily intended for tests and diagnostics.
func (c *NavigationStrategyChain) Strategies() []NavigationStrategy {
	out := make([]NavigationStrategy, len(c.strategies))
	copy(out, c.strategies)
	return out
}

// Restore walks the chain by priority. It returns the first successful
// non-empty PageHash. If no strategy applies, ErrNoNavigationPossible is
// returned. A failure from the final strategy is propagated; failures of
// earlier strategies are logged and the next strategy is tried.
func (c *NavigationStrategyChain) Restore(nctx *NavigationContext) (string, error) {
	last := len(c.strategies) - 1
	for i, strategy := range c.strategies {
		result, err := strategy.Restore(nctx)
		if err != nil {
			if i == last {
				return "", err
			}
			if c.logger != nil {
				c.logger.Debug("Navigation strategy failed, trying next",
					slog.String("strategy", strategy.Name()),
					slog.String("error", err.Error()),
				)
			}
			continue
		}
		if result.PageHash != "" {
			if c.logger != nil {
				c.logger.Debug("Navigation strategy succeeded",
					slog.String("strategy", strategy.Name()),
					slog.String("page_hash", result.PageHash),
				)
			}
			return result.PageHash, nil
		}
		if c.logger != nil {
			c.logger.Debug("Navigation strategy not applicable, trying next",
				slog.String("strategy", strategy.Name()),
			)
		}
	}
	return "", ErrNoNavigationPossible
}
