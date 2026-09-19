package crawler

import (
	"log/slog"

	graphlib "github.com/dominikbraun/graph"
	"github.com/pkg/errors"
	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/diagnostics"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
)

// Strategy 1: if the action's target element is already visible (and
// identity-matches) on the current page, no navigation is required at all.
type elementVisibilityStrategy struct{ c *Crawler }

// NewElementVisibilityStrategy restores an origin state by detecting that the
// action's target element is already present and interactable on the current
// page.
func NewElementVisibilityStrategy(c *Crawler) NavigationStrategy {
	return elementVisibilityStrategy{c: c}
}

func (elementVisibilityStrategy) Name() string { return "element-visibility" }

func (s elementVisibilityStrategy) Restore(nctx *NavigationContext) (NavigationResult, error) {
	// The element shortcut only applies to element actions on non-blank pages.
	if nctx.Action.Element == nil || nctx.CurrentHash == emptyPageHash {
		return NavigationResult{}, nil
	}
	newPageHash, err := s.c.tryElementNavigation(nctx.Page, nctx.Action, nctx.CurrentHash)
	if err != nil {
		return NavigationResult{}, err
	}
	return NavigationResult{PageHash: newPageHash}, nil
}

// Strategy 2: walk the browser's own history backwards when the origin entry
// is still recorded there.
type browserHistoryStrategy struct{ c *Crawler }

// NewBrowserHistoryStrategy restores an origin state by issuing browser
// history back-steps.
func NewBrowserHistoryStrategy(c *Crawler) NavigationStrategy {
	return browserHistoryStrategy{c: c}
}

func (browserHistoryStrategy) Name() string { return "browser-history" }

func (s browserHistoryStrategy) Restore(nctx *NavigationContext) (NavigationResult, error) {
	newPageHash, err := s.c.tryBrowserHistoryNavigation(nctx.Page, nctx.Origin, nctx.Action)
	if err != nil {
		return NavigationResult{}, err
	}
	return NavigationResult{PageHash: newPageHash}, nil
}

// Strategy 3 (last-resort): replay the shortest action path in the crawl
// graph, falling back to the blank state when the current vertex cannot reach
// the origin.
type shortestPathStrategy struct{ c *Crawler }

// NewShortestPathStrategy restores an origin state by replaying the shortest
// path of recorded actions in the crawl graph. It is the terminal fallback
// strategy.
func NewShortestPathStrategy(c *Crawler) NavigationStrategy {
	return shortestPathStrategy{c: c}
}

func (shortestPathStrategy) Name() string { return "shortest-path" }

func (s shortestPathStrategy) Restore(nctx *NavigationContext) (NavigationResult, error) {
	newPageHash, err := s.c.tryShortestPathNavigation(nctx.Action, nctx.Page, nctx.CurrentHash)
	if err != nil {
		return NavigationResult{}, err
	}
	return NavigationResult{PageHash: newPageHash}, nil
}

// defaultNavigationStrategies returns the built-in priority order:
// element visibility -> browser history -> shortest-path BFS.
func defaultNavigationStrategies(c *Crawler) []NavigationStrategy {
	return []NavigationStrategy{
		NewElementVisibilityStrategy(c),
		NewBrowserHistoryStrategy(c),
		NewShortestPathStrategy(c),
	}
}

// The methods below are the concrete navigation primitives used by the
// strategies. They were previously called directly from the hard-coded
// if-else chain in navigateBackToStateOrigin.

func (c *Crawler) tryElementNavigation(page *browser.BrowserPage, action *types.Action, currentPageHash string) (string, error) {
	element, err := page.ElementX(action.Element.XPath)
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
	htmlElement, err := page.GetElementFromXpath(action.Element.XPath)
	if err != nil {
		return "", err
	}
	// Ensure its the same element with stronger identity matching
	if isElementMatch(htmlElement, action.Element) {
		c.logger.Debug("Found target element on current page, proceeding without navigation")
		// FIXME: Return the origin element ID so that the graph shows
		// correctly the fastest way to reach the state.
		return action.OriginID, nil
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

func (c *Crawler) tryBrowserHistoryNavigation(page *browser.BrowserPage, originPageState *types.PageState, action *types.Action) (string, error) {
	canNavigateBack, stepsBack, err := c.isBackNavigationPossible(page, originPageState)
	if err != nil {
		return "", err
	}
	if !canNavigateBack {
		return "", nil
	}

	c.logger.Debug("Navigating back using browser history", slog.Int("steps_back", stepsBack))

	var navigatedSuccessfully bool
	for i := 0; i < stepsBack; i++ {
		err := runWithNavigateBackHook(c.options.Hooks, page, page.NavigateBack)
		if err != nil {
			return "", err
		}
		navigatedSuccessfully = true
	}

	if !navigatedSuccessfully {
		return "", nil
	}

	if err := page.WaitPageLoadHeurisitics(); err != nil {
		c.logger.Debug("Failed to wait for page load after navigating back using browser history", slog.String("error", err.Error()))
	}
	newPageHash, pageState, err := c.isCorrectNavigation(page, action)
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

func (c *Crawler) tryShortestPathNavigation(action *types.Action, page *browser.BrowserPage, currentPageHash string) (string, error) {
	c.logger.Debug("Trying Shortest path to navigate back to origin page", slog.String("action_origin_id", action.OriginID), slog.String("current_page_hash", currentPageHash))

	actions, err := c.crawlGraph.ShortestPath(currentPageHash, action.OriginID)
	if err != nil {
		if errors.Is(err, graphlib.ErrTargetNotReachable) {
			c.logger.Debug("Target not reachable, reaching from blank state",
				slog.String("action_origin_id", action.OriginID),
			)

			actions, err = c.crawlGraph.ShortestPath(emptyPageHash, action.OriginID)
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
		if err := c.executeCrawlStateAction(action, page); err != nil {
			return "", err
		}
	}
	newPageHash, pageState, err := c.isCorrectNavigation(page, action)
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
