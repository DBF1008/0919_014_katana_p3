package crawler

import (
	"log/slog"

	graphlib "github.com/dominikbraun/graph"
	"github.com/pkg/errors"
	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/diagnostics"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
)

// NavigationContext carries the inputs shared by all navigation strategies
// during a state-restoration attempt.
type NavigationContext struct {
	// Action is the crawl action whose origin state must be restored.
	Action *types.Action
	// Page is the browser page to navigate.
	Page *browser.BrowserPage
	// CurrentPageHash is the hash of the page the browser is currently on.
	CurrentPageHash string
	// OriginPageState is the graph state the action was discovered in.
	OriginPageState *types.PageState
}

// NavigationStrategy is a single strategy for navigating the browser back to
// the page state in which a crawl action was originally discovered.
//
// Navigate returns the hash of the page reached after the attempt. An empty
// hash with a nil error means the strategy is not applicable or did not
// succeed, and the chain falls through to the next strategy.
type NavigationStrategy interface {
	// Name identifies the strategy for logging and diagnostics.
	Name() string
	// Navigate attempts the state restoration.
	Navigate(nav *NavigationContext) (string, error)
}

// NavigationChain tries an ordered list of navigation strategies by
// priority. The first strategy that produces a non-empty page hash wins.
// Errors from non-terminal strategies are logged and skipped so the next
// strategy can take over; an error from the terminal strategy is propagated
// to the caller. If no strategy succeeds, ErrNoNavigationPossible is
// returned.
type NavigationChain struct {
	logger     *slog.Logger
	strategies []NavigationStrategy
}

// NewNavigationChain builds a chain that tries the given strategies in
// order. The last strategy is treated as terminal: its error is propagated
// instead of being skipped.
func NewNavigationChain(logger *slog.Logger, strategies ...NavigationStrategy) *NavigationChain {
	if logger == nil {
		logger = slog.Default()
	}
	return &NavigationChain{logger: logger, strategies: strategies}
}

// Navigate walks the strategy chain until a strategy restores the origin
// state or all strategies are exhausted.
func (chain *NavigationChain) Navigate(nav *NavigationContext) (string, error) {
	for i, strategy := range chain.strategies {
		terminal := i == len(chain.strategies)-1
		newPageHash, err := strategy.Navigate(nav)
		if err != nil {
			if terminal {
				return "", err
			}
			chain.logger.Debug("Navigation strategy failed, trying next",
				slog.String("strategy", strategy.Name()),
				slog.String("error", err.Error()),
			)
			continue
		}
		if newPageHash != "" {
			return newPageHash, nil
		}
	}
	return "", ErrNoNavigationPossible
}

// defaultNavigationChain builds the crawler's state-restoration chain:
//
//  1. ElementVisibilityStrategy — if the action's element is visible on the
//     current page, use it directly without navigating.
//  2. BrowserHistoryStrategy — if the origin page is in the browser history,
//     navigate back through it.
//  3. ShortestPathStrategy — replay the shortest action path from the graph
//     root (terminal fallback).
func defaultNavigationChain(c *Crawler) *NavigationChain {
	return NewNavigationChain(c.logger,
		&ElementVisibilityStrategy{c: c},
		&BrowserHistoryStrategy{c: c},
		&ShortestPathStrategy{c: c},
	)
}

// ElementVisibilityStrategy checks whether the element the action wants to
// interact with already exists (visible and interactable) on the current
// page, avoiding a navigation round-trip entirely.
type ElementVisibilityStrategy struct{ c *Crawler }

func (s *ElementVisibilityStrategy) Name() string { return "element-visibility" }

func (s *ElementVisibilityStrategy) Navigate(nav *NavigationContext) (string, error) {
	if nav.Action.Element == nil || nav.CurrentPageHash == emptyPageHash {
		return "", nil
	}
	c := s.c

	element, err := nav.Page.ElementX(nav.Action.Element.XPath)
	if err != nil {
		return "", err
	}
	visible, err := element.Visible()
	if err != nil {
		return "", err
	}
	if !visible {
		return "", nil
	}

	// Also ensure its interactable
	interactable, err := element.Interactable()
	if err != nil || interactable == nil {
		return "", nil
	}

	// Ensure its the same element
	htmlElement, err := nav.Page.GetElementFromXpath(nav.Action.Element.XPath)
	if err != nil {
		return "", err
	}
	// Ensure its the same element with stronger identity matching
	if isElementMatch(htmlElement, nav.Action.Element) {
		c.logger.Debug("Found target element on current page, proceeding without navigation")
		// FIXME: Return the origin element ID so that the graph shows
		// correctly the fastest way to reach the state.
		return nav.Action.OriginID, nil
	}
	return "", nil
}

// isElementMatch implements stronger identity matching logic to reduce false positives.
// It treats identical ID as definitive match, otherwise requires both Classes and TextContent
// to match, or enforces at least two matching non-empty attributes.
func isElementMatch(current, target *types.HTMLElement) bool {
	if current == nil || target == nil {
		return false
	}
	// Definitive match: identical non-empty IDs
	if current.ID != "" && target.ID != "" && current.ID == target.ID {
		return true
	}
	matchCount := 0

	if current.Classes != "" && target.Classes != "" && current.Classes == target.Classes {
		matchCount++
	}
	if current.TextContent != "" && target.TextContent != "" && current.TextContent == target.TextContent {
		matchCount++
	}
	if current.TagName != "" && target.TagName != "" && current.TagName == target.TagName {
		matchCount++
	}
	// Require at least two matching non-empty attributes for a positive match
	// This ensures stronger identity verification while still allowing reasonable fallbacks
	return matchCount >= 2
}

// BrowserHistoryStrategy navigates back through the browser history when the
// origin page (matched by URL and title) is present in it.
type BrowserHistoryStrategy struct{ c *Crawler }

func (s *BrowserHistoryStrategy) Name() string { return "browser-history" }

func (s *BrowserHistoryStrategy) Navigate(nav *NavigationContext) (string, error) {
	c := s.c
	canNavigateBack, stepsBack, err := c.isBackNavigationPossible(nav.Page, nav.OriginPageState)
	if err != nil {
		return "", err
	}
	if !canNavigateBack {
		return "", nil
	}

	c.logger.Debug("Navigating back using browser history", slog.Int("steps_back", stepsBack))

	var navigatedSuccessfully bool
	for i := 0; i < stepsBack; i++ {
		err := runWithNavigateBackHook(c.options.Hooks, nav.Page, nav.Page.NavigateBack)
		if err != nil {
			return "", err
		}
		navigatedSuccessfully = true
	}

	if !navigatedSuccessfully {
		return "", nil
	}

	if err := nav.Page.WaitPageLoadHeurisitics(); err != nil {
		c.logger.Debug("Failed to wait for page load after navigating back using browser history", slog.String("error", err.Error()))
	}
	newPageHash, pageState, err := c.isCorrectNavigation(nav.Page, nav.Action)
	if c.diagnostics != nil && pageState != nil {
		if err := c.diagnostics.LogPageState(pageState, diagnostics.PreActionPageState); err != nil {
			return "", err
		}
	}
	if err != nil {
		return "", err
	}
	return newPageHash, nil
}

func (c *Crawler) isBackNavigationPossible(page *browser.BrowserPage, originPage *types.PageState) (bool, int, error) {
	history, err := page.GetNavigationHistory()
	if err != nil {
		return false, 0, err
	}
	if len(history.Entries) == 0 {
		return false, 0, nil
	}

	currentIndex := history.CurrentIndex
	for i, entry := range history.Entries {
		if entry.URL == originPage.URL && originPage.Title == entry.Title {
			stepsBack := currentIndex - i
			return true, stepsBack, nil
		}
	}
	return false, 0, nil
}

// ShortestPathStrategy replays the shortest action path from the current
// state (or the blank root state) to the action's origin state, using the
// crawl graph. It is the terminal fallback of the chain.
type ShortestPathStrategy struct{ c *Crawler }

func (s *ShortestPathStrategy) Name() string { return "shortest-path" }

func (s *ShortestPathStrategy) Navigate(nav *NavigationContext) (string, error) {
	c := s.c
	c.logger.Debug("Trying Shortest path to navigate back to origin page",
		slog.String("action_origin_id", nav.Action.OriginID),
		slog.String("current_page_hash", nav.CurrentPageHash),
	)

	actions, err := c.crawlGraph.ShortestPath(nav.CurrentPageHash, nav.Action.OriginID)
	if err != nil {
		if errors.Is(err, graphlib.ErrTargetNotReachable) {
			c.logger.Debug("Target not reachable, reaching from blank state",
				slog.String("action_origin_id", nav.Action.OriginID),
			)

			actions, err = c.crawlGraph.ShortestPath(emptyPageHash, nav.Action.OriginID)
			if err != nil {
				return "", errors.Wrap(err, "could not find path to origin page")
			}
		} else {
			return "", errors.Wrap(err, "failed to find shortest path")
		}
	}
	c.logger.Debug("Found actions to traverse",
		slog.Any("actions", actions),
	)
	for _, action := range actions {
		if err := c.executeCrawlStateAction(action, nav.Page); err != nil {
			return "", err
		}
	}
	newPageHash, pageState, err := c.isCorrectNavigation(nav.Page, nav.Action)
	if c.diagnostics != nil && pageState != nil {
		if err := c.diagnostics.LogPageState(pageState, diagnostics.PreActionPageState); err != nil {
			return "", err
		}
	}
	if err != nil {
		return "", err
	}
	return newPageHash, nil
}
