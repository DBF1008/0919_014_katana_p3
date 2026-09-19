package crawler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"

	"github.com/pkg/errors"
	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/normalizer/simhash"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
)

var emptyPageHash = sha256Hash("")

const simhashThreshold = 2 // Allow up to 2 bits difference

func (c *Crawler) isCorrectNavigation(page *browser.BrowserPage, action *types.Action) (string, *types.PageState, error) {
	currentPageHash, pageState, err := getPageHash(page)
	if err != nil {
		return "", nil, err
	}

	if currentPageHash == action.OriginID {
		return currentPageHash, pageState, nil
	}

	// Get the origin page state to compare SimHash
	originPageState, err := c.crawlGraph.GetPageState(action.OriginID)
	if err != nil {
		return "", pageState, fmt.Errorf("failed to get origin page state: %w", err)
	}

	if pageState != nil && originPageState != nil {
		distance := simhash.Distance(pageState.SimHash, originPageState.SimHash)
		if distance <= simhashThreshold {
			c.logger.Debug("Page is similar enough to origin, proceeding",
				slog.String("current_hash", currentPageHash),
				slog.String("origin_hash", action.OriginID),
				slog.Uint64("simhash_distance", uint64(distance)),
			)
			// Treat this page as the origin state to avoid creating a new vertex
			return originPageState.UniqueID, pageState, nil
		}
	}

	return "", pageState, fmt.Errorf("failed to navigate back to origin page: %s != %s", currentPageHash, action.OriginID)
}

func getPageHash(page *browser.BrowserPage) (string, *types.PageState, error) {
	pageState, err := newPageState(page, nil)
	if err == ErrEmptyPage {
		return emptyPageHash, nil, nil
	}
	if err != nil {
		return "", nil, errors.Wrap(err, "could not get page state")
	}
	return pageState.UniqueID, pageState, nil
}

var ErrEmptyPage = errors.New("page is empty")

func newPageState(page *browser.BrowserPage, action *types.Action) (*types.PageState, error) {
	pageInfo, err := page.Info()
	if err != nil {
		return nil, errors.Wrap(err, "could not get page info")
	}
	if pageInfo.URL == "" || pageInfo.URL == "about:blank" {
		return nil, ErrEmptyPage
	}

	outerHTML, err := page.HTML()
	if err != nil {
		return nil, errors.Wrap(err, "could not get html content")
	}

	state := &types.PageState{
		URL:              pageInfo.URL,
		DOM:              outerHTML,
		NavigationAction: action,
		Title:            pageInfo.Title,
	}
	if action != nil {
		state.Depth = action.Depth + 1
	}
	strippedDOM, err := getStrippedDOM(outerHTML)
	if err != nil {
		return nil, errors.Wrap(err, "could not get stripped dom")
	}
	state.StrippedDOM = strippedDOM

	// Get sha256 hash of the stripped dom
	state.UniqueID = sha256Hash(strippedDOM)
	state.SimHash = simhash.Fingerprint(strings.NewReader(strippedDOM), 3)

	return state, nil
}

func sha256Hash(item string) string {
	hasher := sha256.New()
	hasher.Write([]byte(item))
	hashItem := hex.EncodeToString(hasher.Sum(nil))
	return hashItem
}

func getStrippedDOM(contents string) (string, error) {
	normalized, err := domNormalizer.Apply(contents)
	if err != nil {
		return "", errors.Wrap(err, "could not normalize dom")
	}
	return normalized, nil
}

var ErrNoNavigationPossible = errors.New("no navigation possible")

// navigateBackToStateOrigin restores the browser to the page state in which
// the action was discovered by delegating to the crawler's NavigationChain.
// The chain tries each registered NavigationStrategy in priority order:
//
//  1. ElementVisibilityStrategy — reuse the action's element if it is still
//     visible on the current page.
//  2. BrowserHistoryStrategy — walk the browser history back to the origin.
//  3. ShortestPathStrategy — replay the shortest action path from the graph.
//
// New strategies can be added to the chain without touching this logic.
func (c *Crawler) navigateBackToStateOrigin(action *types.Action, page *browser.BrowserPage, currentPageHash string) (string, error) {
	c.logger.Debug("Found action with different origin id",
		slog.String("action_origin_id", action.OriginID),
		slog.String("current_page_hash", currentPageHash),
	)

	// Get vertex from the graph
	originPageState, err := c.crawlGraph.GetPageState(action.OriginID)
	if err != nil {
		c.logger.Debug("Failed to get origin page state", slog.String("error", err.Error()))
		return "", err
	}

	return c.navigationChain().Navigate(&NavigationContext{
		Action:          action,
		Page:            page,
		CurrentPageHash: currentPageHash,
		OriginPageState: originPageState,
	})
}

// navigationChain returns the configured strategy chain, falling back to the
// default chain (element visibility → browser history → shortest path) when
// none was installed.
func (c *Crawler) navigationChain() *NavigationChain {
	if c.navChain != nil {
		return c.navChain
	}
	return defaultNavigationChain(c)
}
