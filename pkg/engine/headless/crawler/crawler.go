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
	"github.com/happyhackingspace/dit"
	"github.com/pkg/errors"
	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/captcha"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/diagnostics"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/normalizer"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/normalizer/simhash"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/pipeline"
	"github.com/projectdiscovery/katana/pkg/engine/headless/graph"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
	"github.com/projectdiscovery/katana/pkg/output"
)

// Crawler drives a headless crawl. The per-action work is performed by a
// channel-connected pipeline of six stages; Crawl itself is only the
// orchestration loop (queue management, context/deadline handling, error
// taxonomy and browser-pool lifecycle).
type Crawler struct {
	logger               *slog.Logger
	launcher             *browser.Launcher
	options              Options
	crawlQueue           queue.Queue[*types.Action]
	crawlGraph           *graph.CrawlGraph
	simhashOracle        *simhash.Oracle
	uniqueActions        map[string]struct{}
	diagnostics          diagnostics.Writer
	loggedIn             bool
	navigationStrategies *NavigationStrategyChain
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

	// NavigationStrategies optionally replaces the default strategy chain
	// used to restore the browser to an action's origin state. When nil the
	// built-in order applies: element-visibility, browser-history,
	// shortest-path. Custom strategies are tried before the shortest-path
	// fallback when DefaultNavigationStrategies are extended.
	NavigationStrategies []NavigationStrategy

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

	if len(opts.NavigationStrategies) > 0 {
		crawler.navigationStrategies = NewNavigationStrategyChain(opts.Logger, opts.NavigationStrategies...)
	} else {
		crawler.navigationStrategies = NewNavigationStrategyChain(opts.Logger, defaultNavigationStrategies(crawler)...)
	}

	return crawler, nil
}

// SetNavigationStrategies replaces the navigation strategy chain used by
// subsequent Crawl invocations. Passing a nil slice restores the built-in
// default order. Not safe to call concurrently with Crawl.
func (c *Crawler) SetNavigationStrategies(strategies []NavigationStrategy) {
	if len(strategies) == 0 {
		c.navigationStrategies = NewNavigationStrategyChain(c.logger, defaultNavigationStrategies(c)...)
		return
	}
	c.navigationStrategies = NewNavigationStrategyChain(c.logger, strategies...)
}

// NavigationStrategies returns the strategies currently registered on the
// chain, in priority order.
func (c *Crawler) NavigationStrategies() []NavigationStrategy {
	return c.navigationStrategies.Strategies()
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

// Crawl runs the orchestration loop. Each dequeued action is handed to the
// six-stage pipeline; see package docs for the stage contracts.
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
	if err := crawlGraph.AddPageState(types.PageState{
		UniqueID: emptyPageHash,
		URL:      "about:blank",
		Depth:    0,
	}); err != nil {
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

	return runWithCrawlHooks(c.options.Hooks, ctx, URL, func() error {
		return c.runPipelineLoop(ctx, crawlQueue, localDeadline, parentCtx)
	})
}

// runPipelineLoop is the orchestration loop: dequeue -> acquire page -> pipeline
// -> classify outcome. Concurrency is currently fixed at one in-flight action,
// matching the previous implementation; the pipeline is ready to fan out later.
func (c *Crawler) runPipelineLoop(ctx context.Context, crawlQueue queue.Queue[*types.Action], localDeadline bool, parentCtx context.Context) error {
	stages := c.buildStages()
	wrapped := make([]pipeline.Stage[*crawlItem], len(stages))
	for i, stage := range stages {
		wrapped[i] = crawlHookedStage{inner: stage, hooks: &c.options.Hooks}
	}
	pipe := pipeline.New[*crawlItem](wrapped...)
	pipe.Run(ctx)
	// Shutdown (rather than a bare channel close) drains any in-flight item
	// so stage goroutines can unwind when the loop exits early.
	defer pipe.Shutdown()

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
			// Check for too many failures
			if c.options.MaxFailureCount > 0 && consecutiveFailures >= c.options.MaxFailureCount {
				c.logger.Warn("Too many consecutive failures, stopping crawl",
					slog.Int("failures", consecutiveFailures),
					slog.Int("max_allowed", c.options.MaxFailureCount),
					slog.Int("remaining_actions", crawlQueue.Size()),
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

			actionErr := c.processAction(ctx, pipe, action, page)
			if actionErr == nil {
				consecutiveFailures = 0
				continue
			}
			if errors.Is(actionErr, ErrNoCrawlingAction) {
				return nil
			}
			classifyPipelineError(c.logger, action.String(), actionErr)
			consecutiveFailures++
		}
	}
}

// processAction sends one item through the pipeline and blocks until it either
// succeeds or fails. The browser page is always returned to the pool.
func (c *Crawler) processAction(ctx context.Context, pipe *pipeline.Pipeline[*crawlItem], action *types.Action, page *browser.BrowserPage) error {
	defer c.launcher.PutBrowserToPool(page)

	select {
	case pipe.Input() <- newCrawlItem(ctx, action, page):
	case <-ctx.Done():
		return nil
	}

	select {
	case <-pipe.Success():
		return nil
	case failed := <-pipe.Errors():
		return failed.Err
	case <-ctx.Done():
		return nil
	}
}

// crawlHookedStage wraps a crawler pipeline stage with BeforeStage and
// AfterStage lifecycle hooks. Skipped items bypass both the stage and hooks
// (handled by the pipeline runner before Process is called).
type crawlHookedStage struct {
	inner pipeline.Stage[*crawlItem]
	hooks *Hooks
}

func (s crawlHookedStage) Name() pipeline.StageName { return s.inner.Name() }

func (s crawlHookedStage) Process(ctx context.Context, item *crawlItem) error {
	return runWithStageHooks(*s.hooks, item.page, item.action, s.inner.Name(), func() error {
		return s.inner.Process(ctx, item)
	})
}

var ErrNoCrawlingAction = errors.New("no more actions to crawl")

var ErrElementNotVisible = errors.New("element not visible")
