package crawler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/pkg/errors"
	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/pipeline"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
)

// NavigatorStage restores the browser to the action's origin state (using the
// NavigationStrategyChain) and then dispatches the action itself.
type navigatorStage struct {
	c *Crawler
}

func (navigatorStage) Name() pipeline.StageName { return StageNavigator }

func (s navigatorStage) Process(ctx context.Context, item *crawlItem) error {
	action := item.action

	if action.OriginID != "" && action.OriginID != item.currentHash {
		s.c.logger.Debug("Need to navigate back to origin",
			slog.String("from", item.currentHash),
			slog.String("to", action.OriginID),
		)
		newPageHash, err := s.c.navigateBackToStateOrigin(ctx, action, item.page, item.currentHash)
		if err != nil {
			return err
		}
		// Refresh the page hash
		item.currentHash = newPageHash
	}

	// FIXME: TODO: Restrict the navigation using scope manager and only
	// proceed with actions if the scope is allowed

	// Record the action before dispatching it.
	if s.c.diagnostics != nil {
		if err := s.c.diagnostics.LogAction(action); err != nil {
			return err
		}
	}
	return s.c.executeCrawlStateAction(action, item.page)
}

func (c *Crawler) executeCrawlStateAction(action *types.Action, page *browser.BrowserPage) error {
	return runWithActionHooks(c.options.Hooks, page, action, func() error {
		return c.dispatchCrawlAction(action, page)
	})
}

func (c *Crawler) dispatchCrawlAction(action *types.Action, page *browser.BrowserPage) error {
	var err error
	switch action.Type {
	case types.ActionTypeLoadURL:
		// Apply a timeout to every critical Rod call.
		pTimeout := page.Timeout(c.options.PageMaxTimeout)

		if err := pTimeout.Navigate(action.Input); err != nil {
			return err
		}
		if err = page.WaitPageLoadHeurisitics(); err != nil {
			return err
		}
	case types.ActionTypeFillForm:
		if err := c.processForm(page, action.Form); err != nil {
			return err
		}
		if err = page.WaitPageLoadHeurisitics(); err != nil {
			return err
		}
	case types.ActionTypeLeftClick, types.ActionTypeLeftClickDown:
		pTimeout := page.Timeout(c.options.PageMaxTimeout)
		element, err := pTimeout.ElementX(action.Element.XPath)
		if err != nil {
			return err
		}

		elementTimeout := element.Timeout(c.options.PageMaxTimeout)
		if err := elementTimeout.ScrollIntoView(); err != nil {
			return err
		}
		visible, err := element.Visible()
		if err != nil {
			return err
		}
		if !visible {
			return ErrElementNotVisible
		}

		// Check if element is interactable (not blocked by overlays)
		interactable, err := element.Interactable()
		if err != nil {
			var ce *rod.CoveredError
			if errors.As(err, &ce) {
				return ErrElementNotVisible
			}
			return err
		}
		if interactable == nil {
			return ErrElementNotVisible
		}

		if err := element.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return err
		}
		if err = page.WaitPageLoadHeurisitics(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown action type: %v", action.Type)
	}

	return nil
}
