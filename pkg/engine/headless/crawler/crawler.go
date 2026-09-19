package crawler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"

	"github.com/adrianbrad/queue"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/rod/lib/utils"
	"github.com/happyhackingspace/dit"
	"github.com/pkg/errors"
	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/captcha"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/diagnostics"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/normalizer"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/normalizer/simhash"
	"github.com/projectdiscovery/katana/pkg/engine/headless/graph"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
	"github.com/projectdiscovery/katana/pkg/output"
)

type Crawler struct {
	logger        *slog.Logger
	launcher      *browser.Launcher
	options       Options
	crawlQueue    queue.Queue[*types.Action]
	crawlGraph    *graph.CrawlGraph
	simhashOracle *simhash.Oracle
	uniqueActions map[string]struct{}
	diagnostics   diagnostics.Writer
	loggedIn      bool
	navChain      *NavigationChain
}

type Options struct {
	Context             context.Context
	ChromiumPath        string
	MaxBrowsers         int
	MaxDepth            int
	PageMaxTimeout      time.Duration
	NoSandbox           bool
	NoIncognito         bool
	ShowBrowser         bool
	SlowMotion          bool
	MaxCrawlDuration    time.Duration
	MaxFailureCount     int
	Trace               bool
	CookieConsentBypass bool
	AutomaticFormFill   bool
	PageLoadStrategy    string
	ChromeWSUrl         string
	DOMWaitTime         int
	UserDataDir         string

	// EnableDiagnostics enables the diagnostics mode
	// which writes diagnostic information to a directory
	// specified by the DiagnosticsDir optionally.
	EnableDiagnostics bool
	DiagnosticsDir    string

	Proxy           string
	Logger          *slog.Logger
	ScopeValidator  browser.ScopeValidator
	RequestCallback func(*output.Result)
	ChromeUser      *user.User
	CaptchaHandler  *captcha.Handler
	UserArguments   map[string]string

	AuthUsername  string
	AuthPassword  string
	DitClassifier *dit.Classifier

	// Hooks installs optional lifecycle callbacks. See Hooks for semantics.
	// The zero value disables all callbacks.
	Hooks Hooks
}

var domNormalizer *normalizer.Normalizer
var initOnce sync.Once
var initError error

func init() {
	initOnce.Do(func() {
		var err error
		domNormalizer, err = normalizer.New()
		if err != nil {
			initError = errors.Wrap(err, "failed to create domnormalizer")
		}
	})
}

func New(opts Options) (*Crawler, error) {
	if initError != nil {
		return nil, initError
	}

	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	launcher, err := browser.NewLauncher(browser.LauncherOptions{
		ChromiumPath:        opts.ChromiumPath,
		MaxBrowsers:         opts.MaxBrowsers,
		PageMaxTimeout:      opts.PageMaxTimeout,
		ShowBrowser:         opts.ShowBrowser,
		RequestCallback:     opts.RequestCallback,
		SlowMotion:          opts.SlowMotion,
		ScopeValidator:      opts.ScopeValidator,
		ChromeUser:          opts.ChromeUser,
		Trace:               opts.Trace,
		CookieConsentBypass: opts.CookieConsentBypass,
		NoSandbox:           opts.NoSandbox,
		NoIncognito:         opts.NoIncognito,
		PageLoadStrategy:    opts.PageLoadStrategy,
		ChromeWSUrl:         opts.ChromeWSUrl,
		DOMWaitTime:         opts.DOMWaitTime,
		UserDataDir:         opts.UserDataDir,
		Proxy:               opts.Proxy,
		UserArguments:       opts.UserArguments,
	})
	if err != nil {
		return nil, err
	}

	var diagnosticsWriter diagnostics.Writer
	if opts.EnableDiagnostics {
		directory := opts.DiagnosticsDir
		if directory == "" {
			cwd, _ := os.Getwd()
			directory = filepath.Join(cwd, fmt.Sprintf("katana-diagnostics-%s", time.Now().Format(time.RFC3339)))
		}

		writer, err := diagnostics.NewWriter(directory)
		if err != nil {
			return nil, err
		}
		diagnosticsWriter = writer
		opts.DiagnosticsDir = directory
		opts.Logger.Info("Diagnostics enabled", slog.String("directory", directory))
	}

	crawler := &Crawler{
		launcher:      launcher,
		options:       opts,
		logger:        opts.Logger,
		uniqueActions: make(map[string]struct{}),
		diagnostics:   diagnosticsWriter,
		simhashOracle: simhash.NewOracle(),
	}
	crawler.navChain = defaultNavigationChain(crawler)
	return crawler, nil
}

func (c *Crawler) Close() {
	c.launcher.Close()
	if c.diagnostics != nil {
		if err := c.diagnostics.Close(); err != nil {
			c.logger.Warn("Failed to close diagnostics", slog.String("error", err.Error()))
		}
	}
}

func (c *Crawler) GetCrawlGraph() *graph.CrawlGraph {
	return c.crawlGraph
}

func (c *Crawler) Crawl(URL string) error {
	defer func() {
		if c.diagnostics == nil {
			return
		}
		err := c.crawlGraph.DrawGraph(filepath.Join(c.options.DiagnosticsDir, "crawl-graph.dot"))
		if err != nil {
			c.logger.Error("Failed to draw crawl graph", slog.String("error", err.Error()))
		}
	}()

	actions := []*types.Action{{
		Type:     types.ActionTypeLoadURL,
		Input:    URL,
		Depth:    0,
		OriginID: emptyPageHash,
	}}

	crawlQueue := queue.NewLinked(actions)
	c.crawlQueue = crawlQueue

	crawlGraph := graph.NewCrawlGraph()
	c.crawlGraph = crawlGraph

	// Add the initial blank state
	err := crawlGraph.AddPageState(types.PageState{
		UniqueID: emptyPageHash,
		URL:      "about:blank",
		Depth:    0,
	})
	if err != nil {
		return err
	}

	// Create a master context that will automatically cancel all page operations
	// once the per-URL crawl deadline is reached.
	parentCtx := c.options.Context
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	var (
		ctx           context.Context
		cancel        context.CancelFunc
		localDeadline bool
	)
	if c.options.MaxCrawlDuration > 0 {
		ctx, cancel = context.WithTimeout(parentCtx, c.options.MaxCrawlDuration)
		localDeadline = true
	} else {
		ctx, cancel = context.WithCancel(parentCtx)
	}
	defer cancel()

	// Start the crawl pipeline: each action flows through the ordered stages
	// (navigator → action processor → captcha → auth → discovery → graph),
	// connected by channels. Items are fed one at a time and the result is
	// collected before the next action is dequeued, keeping stage execution
	// strictly sequential over shared crawler state.
	pipeline := NewPipeline(c.options.Hooks, c.stages()...)
	itemCh := make(chan *WorkItem)
	results := pipeline.Run(ctx, itemCh)
	defer func() {
		close(itemCh)
		for range results {
		}
		pipeline.Wait()
	}()

	consecutiveFailures := 0

	for {
		select {
		case <-ctx.Done():
			// Distinguish internal max-duration from external parent cancellation
			if localDeadline && parentCtx.Err() == nil {
				c.logger.Debug("Max crawl duration reached, stopping crawl")
				return nil
			}
			c.logger.Debug("Context cancelled, stopping headless crawl")
			return ctx.Err()
		default:
		}

		// Check for too many failures
		if c.options.MaxFailureCount > 0 && consecutiveFailures >= c.options.MaxFailureCount {
			c.logger.Warn("Too many consecutive failures, stopping crawl",
				slog.Int("failures", consecutiveFailures),
				slog.Int("max_allowed", c.options.MaxFailureCount),
				slog.Int("remaining_actions", c.crawlQueue.Size()),
			)
			return nil
		}

		action, err := crawlQueue.Get()
		if err == queue.ErrNoElementsAvailable {
			c.logger.Debug("No more actions to process")
			return nil
		}
		if err != nil {
			return err
		}

		if c.options.MaxDepth > 0 && action.Depth > c.options.MaxDepth {
			continue
		}

		page, err := c.launcher.GetPageFromPool()
		if err != nil {
			return err
		}

		page.Page = page.Context(ctx)

		c.logger.Debug("Processing action",
			slog.String("action", action.String()),
		)

		item := &WorkItem{Action: action, Page: page}
		select {
		case itemCh <- item:
		case <-ctx.Done():
			c.launcher.PutBrowserToPool(page)
			continue
		}

		var result *WorkItem
		select {
		case result = <-results:
			if result == nil {
				// Pipeline shut down (context cancelled); the loop's
				// ctx check above handles the exit.
				continue
			}
		case <-ctx.Done():
			// The pipeline drops in-flight items on cancellation; the page
			// is intentionally not returned to the pool since the crawl is
			// shutting down.
			continue
		}
		c.launcher.PutBrowserToPool(page)

		if result.Err != nil {
			if errors.Is(result.Err, ErrNoCrawlingAction) {
				return nil
			}
			c.logActionError(result)
			consecutiveFailures++
			continue
		}

		consecutiveFailures = 0
	}
}

var ErrNoCrawlingAction = errors.New("no more actions to crawl")

// logActionError classifies a failed work item and logs it under the
// matching category. All classified errors are skippable: the caller counts
// them towards the consecutive-failure budget and continues the crawl.
func (c *Crawler) logActionError(item *WorkItem) {
	err := item.Err
	action := item.Action
	stage := slog.String("stage", item.FailedStage)

	var npe *rod.NoPointerEventsError
	var ish *rod.InvisibleShapeError
	var ne *rod.NavigationError
	var msce *utils.MaxSleepCountError
	switch {
	case errors.Is(err, ErrElementNotVisible):
		// Counted as a consecutive failure without additional logging.
	case errors.As(err, &npe) || errors.As(err, &ish):
		c.logger.Debug("Skipping action as it is not visible",
			slog.String("action", action.String()),
			slog.String("error", err.Error()),
			stage,
		)
	case errors.As(err, &ne):
		c.logger.Debug("Skipping action as navigation failed",
			slog.String("action", action.String()),
			slog.String("error", err.Error()),
			stage,
		)
	case errors.Is(err, ErrNoNavigationPossible):
		c.logger.Debug("Skipping action as no navigation possible",
			slog.String("action", action.String()),
			stage,
		)
	case errors.As(err, &msce):
		c.logger.Debug("Skipping action as it is taking too long",
			slog.String("action", action.String()),
			stage,
		)
	default:
		c.logger.Debug("Skipping action due to site-specific error",
			slog.String("error", err.Error()),
			slog.String("action", action.String()),
			stage,
		)
	}
}

var ErrElementNotVisible = errors.New("element not visible")

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
