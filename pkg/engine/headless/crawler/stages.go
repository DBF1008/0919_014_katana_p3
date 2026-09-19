package crawler

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/go-rod/rod/lib/proto"
	"github.com/projectdiscovery/gologger"
	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/diagnostics"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
)

// Stage names, in pipeline execution order.
const (
	StageNavigator          = "navigator"
	StageActionProcessor    = "action-processor"
	StageCaptchaHandler     = "captcha-handler"
	StageAuthHandler        = "auth-handler"
	StageDiscoveryCollector = "discovery-collector"
	StageGraphWriter        = "graph-writer"
)

// stages returns the ordered crawl pipeline stages for this crawler.
func (c *Crawler) stages() []Stage {
	return []Stage{
		&NavigatorStage{c: c},
		&ActionProcessorStage{c: c},
		&CaptchaStage{c: c},
		&AuthStage{c: c},
		&DiscoveryStage{c: c},
		&GraphWriterStage{c: c},
	}
}

// NavigatorStage restores the browser to the page state in which the action
// was originally discovered, so the action executes against the same DOM it
// was extracted from.
type NavigatorStage struct{ c *Crawler }

func (s *NavigatorStage) Name() string { return StageNavigator }

func (s *NavigatorStage) Process(_ context.Context, item *WorkItem) error {
	currentPageHash, _, err := getPageHash(item.Page)
	if err != nil {
		return err
	}

	s.c.logger.Debug("Processing action - current state",
		slog.String("current_page_hash", currentPageHash),
		slog.String("action_origin_id", item.Action.OriginID),
		slog.String("action", item.Action.String()),
	)

	if item.Action.OriginID != "" && item.Action.OriginID != currentPageHash {
		s.c.logger.Debug("Need to navigate back to origin",
			slog.String("from", currentPageHash),
			slog.String("to", item.Action.OriginID),
		)
		newPageHash, err := s.c.navigateBackToStateOrigin(item.Action, item.Page, currentPageHash)
		if err != nil {
			return err
		}
		// Refresh the page hash
		currentPageHash = newPageHash
	}
	item.CurrentPageHash = currentPageHash
	return nil
}

// ActionProcessorStage dispatches the crawl action (load URL, fill form,
// click, ...) against the restored page state.
type ActionProcessorStage struct{ c *Crawler }

func (s *ActionProcessorStage) Name() string { return StageActionProcessor }

func (s *ActionProcessorStage) Process(_ context.Context, item *WorkItem) error {
	// FIXME: TODO: Restrict the navigation using scope manager and only
	// proceed with actions if the scope is allowed
	if s.c.diagnostics != nil {
		if err := s.c.diagnostics.LogAction(item.Action); err != nil {
			return err
		}
	}
	return s.c.executeCrawlStateAction(item.Action, item.Page)
}

// CaptchaStage detects captcha pages after navigation and attempts to solve
// them. When a captcha was handled, remaining stages are skipped: the
// discovered links/forms would belong to the captcha widget, not the real
// page.
type CaptchaStage struct{ c *Crawler }

func (s *CaptchaStage) Name() string { return StageCaptchaHandler }

func (s *CaptchaStage) Process(ctx context.Context, item *WorkItem) error {
	handler := s.c.options.CaptchaHandler
	if handler == nil {
		return nil
	}
	html, err := item.Page.HTML()
	if err != nil {
		return nil
	}
	handled, solveErr := handler.HandleIfCaptcha(ctx, item.Page.Page, html)
	if solveErr != nil {
		gologger.Warning().Msgf("captcha solving failed: %s", solveErr)
	}
	if handled && solveErr == nil {
		_ = item.Page.WaitPageLoadHeurisitics()
	}
	if handled {
		// Skip navigation discovery on captcha pages — the discovered
		// links/forms belong to the captcha widget, not the real page.
		item.Terminated = true
	}
	return nil
}

// AuthStage attempts a one-time automatic login when a login form is
// detected on an in-scope page and credentials are configured.
type AuthStage struct{ c *Crawler }

func (s *AuthStage) Name() string { return StageAuthHandler }

func (s *AuthStage) Process(_ context.Context, item *WorkItem) error {
	c := s.c
	if c.loggedIn || c.options.AuthUsername == "" || c.options.DitClassifier == nil {
		return nil
	}
	info, err := item.Page.Info()
	if err != nil {
		return nil
	}
	if c.options.ScopeValidator != nil && !c.options.ScopeValidator(info.URL) {
		return nil
	}
	html, err := item.Page.HTML()
	if err != nil {
		return nil
	}
	if c.tryAutoLogin(item.Page, html) {
		_ = item.Page.WaitPageLoadHeurisitics()
	}
	return nil
}

// DiscoveryStage builds the post-action page state and collects new
// navigation actions from it, enqueueing the unique, in-scope, non-logout
// ones for further crawling.
type DiscoveryStage struct{ c *Crawler }

func (s *DiscoveryStage) Name() string { return StageDiscoveryCollector }

func (s *DiscoveryStage) Process(_ context.Context, item *WorkItem) error {
	c := s.c

	pageState, err := newPageState(item.Page, item.Action)
	if err != nil {
		return err
	}
	if c.diagnostics != nil {
		if err := c.diagnostics.LogPageState(pageState, diagnostics.PostActionPageState); err != nil {
			return err
		}
	}
	pageState.OriginID = item.CurrentPageHash
	item.PageState = pageState

	if c.options.ScopeValidator != nil && !c.options.ScopeValidator(pageState.URL) {
		c.logger.Debug("Skipping navigation collection - current page is out of scope",
			slog.String("url", pageState.URL),
		)
		item.Terminated = true
		if c.crawlQueue.Size() == 0 {
			return ErrNoCrawlingAction
		}
		return nil
	}

	navigations, err := item.Page.FindNavigations()
	if err != nil {
		return err
	}
	item.Navigations = navigations

	// Log navigations for diagnostics
	if c.diagnostics != nil {
		screenshotState, err := item.Page.Screenshot(false, &proto.PageCaptureScreenshot{
			Format: proto.PageCaptureScreenshotFormatPng,
		})
		if err != nil {
			c.logger.Error("Failed to take screenshot", slog.String("error", err.Error()))
		}
		if err := c.diagnostics.LogPageStateScreenshot(pageState.UniqueID, screenshotState); err != nil {
			c.logger.Error("Failed to log page state screenshot", slog.String("error", err.Error()))
		}
		if err := c.diagnostics.LogNavigations(pageState.UniqueID, navigations); err != nil {
			c.logger.Error("Failed to log navigations", slog.String("error", err.Error()))
		}
	}

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
			return err
		}
	}
	return nil
}

// GraphWriterStage records the visited page state in the crawl graph and
// signals the end of the crawl when nothing remains to be processed.
type GraphWriterStage struct{ c *Crawler }

func (s *GraphWriterStage) Name() string { return StageGraphWriter }

func (s *GraphWriterStage) Process(_ context.Context, item *WorkItem) error {
	c := s.c
	if err := c.crawlGraph.AddPageState(*item.PageState); err != nil {
		return err
	}

	// TODO: Check if the page opened new sub pages and if so capture their
	// navigation as well as close them so the state change can work.

	if len(item.Navigations) == 0 && c.crawlQueue.Size() == 0 {
		return ErrNoCrawlingAction
	}
	return nil
}

func (c *Crawler) tryAutoLogin(page *browser.BrowserPage, html string) bool {
	pageResult, err := c.options.DitClassifier.ExtractPageType(html)
	if err != nil || pageResult == nil {
		return false
	}

	for _, form := range pageResult.Forms {
		if form.Type != "login" {
			continue
		}

		pageURL := ""
		if info, err := page.Info(); err == nil {
			pageURL = info.URL
		}
		c.logger.Info("Login form detected, attempting auto-login",
			slog.String("url", pageURL),
		)

		filled := false
		for fieldName, fieldType := range form.Fields {
			var value string
			switch fieldType {
			case "password":
				value = c.options.AuthPassword
			default:
				value = c.options.AuthUsername
			}

			escapedName := strings.ReplaceAll(fieldName, `\`, `\\`)
			escapedName = strings.ReplaceAll(escapedName, `'`, `\'`)
			el, err := page.Element("input[name='" + escapedName + "']")
			if err != nil {
				c.logger.Debug("Could not find login field", slog.String("field", fieldName))
				continue
			}
			if err := el.Input(value); err != nil {
				c.logger.Debug("Could not fill login field", slog.String("field", fieldName))
				continue
			}
			filled = true
		}

		if !filled {
			continue
		}

		if submitted := c.submitLoginForm(page); submitted {
			c.loggedIn = true
			c.logger.Info("Auto-login submitted successfully")
			return true
		}
	}
	return false
}

func (c *Crawler) submitLoginForm(page *browser.BrowserPage) bool {
	selectors := []string{
		"form button[type='submit']",
		"form input[type='submit']",
		"form button:not([type])",
	}
	for _, sel := range selectors {
		if el, err := page.Element(sel); err == nil {
			if err := el.Click(proto.InputMouseButtonLeft, 1); err == nil {
				return true
			}
		}
	}
	return false
}

var logoutPattern = regexp.MustCompile(`(?i)(log[\s-]?out|sign[\s-]?out|signout|deconnexion|cerrar[\s-]?sesion|sair|abmelden|uitloggen|ausloggen|exit|disconnect|terminate|end[\s-]?session|salir|desconectar|afmelden|wyloguj|logout|sign[\s-]?off)`)

func isLogoutPage(element *types.HTMLElement) bool {
	return logoutPattern.MatchString(element.TextContent) ||
		logoutPattern.MatchString(element.Attributes["href"])
}
