package crawler

import (
	"context"
	"log/slog"
	"regexp"

	"github.com/go-rod/rod/lib/proto"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/diagnostics"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/pipeline"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
)

// DiscoveryCollectorStage builds the post-action page state, enforces scope,
// collects page navigations and enqueues de-duplicated follow-up actions.
type discoveryCollectorStage struct {
	c *Crawler
}

func (discoveryCollectorStage) Name() pipeline.StageName { return StageDiscoveryCollector }

func (s discoveryCollectorStage) Process(_ context.Context, item *crawlItem) error {
	c := s.c
	page := item.page
	action := item.action

	pageState, err := newPageState(page, action)
	if err != nil {
		return err
	}
	if c.diagnostics != nil {
		if err := c.diagnostics.LogPageState(pageState, diagnostics.PostActionPageState); err != nil {
			return err
		}
	}
	pageState.OriginID = item.currentHash
	item.pageState = pageState

	if c.options.ScopeValidator != nil && !c.options.ScopeValidator(pageState.URL) {
		c.logger.Debug("Skipping navigation collection - current page is out of scope",
			slog.String("url", pageState.URL),
		)
		item.SkipRemaining()
		if c.crawlQueue.Size() == 0 {
			return ErrNoCrawlingAction
		}
		return nil
	}

	navigations, err := page.FindNavigations()
	if err != nil {
		return err
	}
	item.navigations = navigations

	// Screenshot + navigation diagnostics are best-effort.
	if c.diagnostics != nil {
		screenshotState, shotErr := page.Screenshot(false, &proto.PageCaptureScreenshot{
			Format: proto.PageCaptureScreenshotFormatPng,
		})
		if shotErr != nil {
			c.logger.Error("Failed to take screenshot", slog.String("error", shotErr.Error()))
		}
		if err := c.diagnostics.LogPageStateScreenshot(pageState.UniqueID, screenshotState); err != nil {
			c.logger.Error("Failed to log page state screenshot", slog.String("error", err.Error()))
		}
		if err := c.diagnostics.LogNavigations(pageState.UniqueID, navigations); err != nil {
			c.logger.Error("Failed to log navigations", slog.String("error", err.Error()))
		}
	}

	c.collectNavigations(pageState, navigations)

	if len(navigations) == 0 && c.crawlQueue.Size() == 0 {
		return ErrNoCrawlingAction
	}
	return nil
}

// collectNavigations de-duplicates navigations and enqueues the survivors.
// It is split out from the stage body so the filtering rules can be tested
// without a live browser.
func (c *Crawler) collectNavigations(pageState *types.PageState, navigations []*types.Action) {
	for _, nav := range navigations {
		actionHash := nav.Hash()
		if _, ok := c.uniqueActions[actionHash]; ok {
			continue
		}
		c.uniqueActions[actionHash] = struct{}{}

		// Check if the element we have is a logout page
		if nav.Element != nil && isLogoutPage(nav.Element) {
			c.logger.Debug("Skipping Found logout page",
				slog.String("url", nav.Element.Attributes["href"]),
			)
			continue
		}
		nav.OriginID = pageState.UniqueID

		c.logger.Debug("Got new navigation",
			slog.Any("navigation", nav),
		)
		if err := c.crawlQueue.Offer(nav); err != nil {
			c.logger.Error("Failed to enqueue navigation", slog.String("error", err.Error()))
		}
	}
}

var logoutPattern = regexp.MustCompile(`(?i)(log[\s-]?out|sign[\s-]?out|signout|deconnexion|cerrar[\s-]?sesion|sair|abmelden|uitloggen|ausloggen|exit|disconnect|terminate|end[\s-]?session|salir|desconectar|afmelden|wyloguj|logout|sign[\s-]?off)`)

func isLogoutPage(element *types.HTMLElement) bool {
	return logoutPattern.MatchString(element.TextContent) ||
		logoutPattern.MatchString(element.Attributes["href"])
}
