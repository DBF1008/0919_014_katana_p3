package crawler

import (
	"context"
	"log/slog"
	"strings"

	"github.com/go-rod/rod/lib/proto"
	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/pipeline"
)

// AuthHandlerStage performs one automatic login attempt per crawl when auth
// credentials and a DIT classifier are configured and a login form is found.
type authHandlerStage struct {
	c *Crawler
}

func (authHandlerStage) Name() pipeline.StageName { return StageAuthHandler }

func (s authHandlerStage) Process(_ context.Context, item *crawlItem) error {
	c := s.c
	if c.loggedIn || c.options.AuthUsername == "" || c.options.DitClassifier == nil {
		return nil
	}

	page := item.page
	info, err := page.Info()
	if err != nil {
		return nil
	}
	if c.options.ScopeValidator != nil && !c.options.ScopeValidator(info.URL) {
		return nil
	}

	html, htmlErr := page.HTML()
	if htmlErr != nil {
		return nil
	}
	if c.tryAutoLogin(page, html) {
		_ = page.WaitPageLoadHeurisitics()
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
