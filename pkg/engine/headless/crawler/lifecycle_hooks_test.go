package crawler

import (
	"context"
	"errors"
	"testing"

	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/crawler/pipeline"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunWithCrawlHooks_Success(t *testing.T) {
	var tr trace
	hooks := Hooks{
		BeforeCrawl: func(_ context.Context, url string) error {
			tr.add("before:" + url)
			return nil
		},
		AfterCrawl: func(_ context.Context, url string, crawlErr error) error {
			assert.NoError(t, crawlErr)
			tr.add("after:" + url)
			return nil
		},
	}

	err := runWithCrawlHooks(hooks, context.Background(), "http://test", func() error {
		tr.add("crawl")
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"before:http://test", "crawl", "after:http://test"}, tr.steps)
}

func TestRunWithCrawlHooks_CrawlErrorPassedToAfter(t *testing.T) {
	var tr trace
	sentinel := errors.New("crawl failed")
	hooks := Hooks{
		AfterCrawl: func(_ context.Context, _ string, crawlErr error) error {
			tr.add("after")
			assert.ErrorIs(t, crawlErr, sentinel)
			return nil
		},
	}

	err := runWithCrawlHooks(hooks, context.Background(), "u", func() error { return sentinel })
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, []string{"after"}, tr.steps)
}

func TestRunWithCrawlHooks_BeforeErrorSkipsCrawlButRunsAfter(t *testing.T) {
	var tr trace
	sentinel := errors.New("before failed")
	hooks := Hooks{
		BeforeCrawl: func(_ context.Context, _ string) error {
			tr.add("before")
			return sentinel
		},
		AfterCrawl: func(_ context.Context, _ string, crawlErr error) error {
			tr.add("after")
			assert.ErrorIs(t, crawlErr, sentinel)
			return nil
		},
	}

	err := runWithCrawlHooks(hooks, context.Background(), "u", func() error {
		tr.add("crawl")
		return nil
	})
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, []string{"before", "after"}, tr.steps, "crawl fn must not run but AfterCrawl must")
}

func TestRunWithCrawlHooks_AfterErrorReturnedWhenCrawlSucceeds(t *testing.T) {
	sentinel := errors.New("after failed")
	hooks := Hooks{
		AfterCrawl: func(_ context.Context, _ string, _ error) error { return sentinel },
	}

	err := runWithCrawlHooks(hooks, context.Background(), "u", func() error { return nil })
	require.ErrorIs(t, err, sentinel)
}

func TestRunWithCrawlHooks_AfterErrorDoesNotMaskCrawlError(t *testing.T) {
	crawlErr := errors.New("crawl")
	afterErr := errors.New("after")
	hooks := Hooks{
		AfterCrawl: func(_ context.Context, _ string, _ error) error { return afterErr },
	}

	err := runWithCrawlHooks(hooks, context.Background(), "u", func() error { return crawlErr })
	require.ErrorIs(t, err, crawlErr)
}

func TestRunWithStageHooks_OrderingOnSuccess(t *testing.T) {
	var tr trace
	hooks := Hooks{
		BeforeStage: func(_ *browser.BrowserPage, _ *types.Action, stage pipeline.StageName) error {
			tr.add("before:" + string(stage))
			return nil
		},
		AfterStage: func(_ *browser.BrowserPage, _ *types.Action, stage pipeline.StageName) error {
			tr.add("after:" + string(stage))
			return nil
		},
	}

	err := runWithStageHooks(hooks, nil, &types.Action{}, StageNavigator, func() error {
		tr.add("process")
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"before:" + string(StageNavigator), "process", "after:" + string(StageNavigator)}, tr.steps)
}

func TestRunWithStageHooks_ProcessErrorSkipsAfter(t *testing.T) {
	var tr trace
	sentinel := errors.New("stage failed")
	hooks := Hooks{
		BeforeStage: func(_ *browser.BrowserPage, _ *types.Action, _ pipeline.StageName) error {
			tr.add("before")
			return nil
		},
		AfterStage: func(_ *browser.BrowserPage, _ *types.Action, _ pipeline.StageName) error {
			tr.add("after")
			return nil
		},
	}

	err := runWithStageHooks(hooks, nil, &types.Action{}, StageGraphWriter, func() error { return sentinel })
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, []string{"before"}, tr.steps)
}

func TestRunWithStageHooks_BeforeErrorSkipsProcessAndAfter(t *testing.T) {
	var tr trace
	sentinel := errors.New("before stage failed")
	hooks := Hooks{
		BeforeStage: func(_ *browser.BrowserPage, _ *types.Action, _ pipeline.StageName) error {
			tr.add("before")
			return sentinel
		},
		AfterStage: func(_ *browser.BrowserPage, _ *types.Action, _ pipeline.StageName) error {
			tr.add("after")
			return nil
		},
	}

	err := runWithStageHooks(hooks, nil, &types.Action{}, StageNavigator, func() error {
		tr.add("process")
		return nil
	})
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, []string{"before"}, tr.steps)
}

func TestStageNameConstants_MatchPipelineOrder(t *testing.T) {
	// Guard the documented ordering: action -> navigator -> captcha -> auth
	// -> discovery -> graph.
	c := &Crawler{}
	stages := c.buildStages()
	require.Len(t, stages, 6)

	want := []pipeline.StageName{
		StageActionProcessor,
		StageNavigator,
		StageCaptchaHandler,
		StageAuthHandler,
		StageDiscoveryCollector,
		StageGraphWriter,
	}
	for i, stage := range stages {
		assert.Equal(t, want[i], stage.Name())
	}
}

func TestCrawlHookedStage_RoutesThroughStageHooks(t *testing.T) {
	var before, after string
	hooks := Hooks{
		BeforeStage: func(_ *browser.BrowserPage, _ *types.Action, stage pipeline.StageName) error {
			before = string(stage)
			return nil
		},
		AfterStage: func(_ *browser.BrowserPage, _ *types.Action, stage pipeline.StageName) error {
			after = string(stage)
			return nil
		},
	}

	inner := pipeline.StageFunc[*crawlItem]{
		StageName: StageAuthHandler,
		Fn:        func(_ context.Context, _ *crawlItem) error { return nil },
	}
	wrapped := crawlHookedStage{inner: inner, hooks: &hooks}

	require.NoError(t, wrapped.Process(context.Background(), &crawlItem{}))
	assert.Equal(t, string(StageAuthHandler), before)
	assert.Equal(t, string(StageAuthHandler), after)
}
