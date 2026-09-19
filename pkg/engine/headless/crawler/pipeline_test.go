package crawler

import (
	"context"
	"errors"
	"testing"

	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStage is a test Stage that records its invocation order and delegates
// to an optional function.
type fakeStage struct {
	name string
	fn   func(ctx context.Context, item *WorkItem) error
}

func (f *fakeStage) Name() string { return f.name }

func (f *fakeStage) Process(ctx context.Context, item *WorkItem) error {
	if f.fn != nil {
		return f.fn(ctx, item)
	}
	return nil
}

// runPipeline feeds the given items through the pipeline and collects the
// processed results until the output channel closes.
func runPipeline(p *Pipeline, items ...*WorkItem) []*WorkItem {
	in := make(chan *WorkItem)
	out := p.Run(context.Background(), in)
	go func() {
		for _, item := range items {
			in <- item
		}
		close(in)
	}()
	var results []*WorkItem
	for item := range out {
		results = append(results, item)
	}
	p.Wait()
	return results
}

func TestPipeline_StagesRunInOrder(t *testing.T) {
	var order []string
	record := func(name string) func(context.Context, *WorkItem) error {
		return func(context.Context, *WorkItem) error {
			order = append(order, name)
			return nil
		}
	}
	p := NewPipeline(Hooks{},
		&fakeStage{name: "first", fn: record("first")},
		&fakeStage{name: "second", fn: record("second")},
		&fakeStage{name: "third", fn: record("third")},
	)

	results := runPipeline(p, &WorkItem{Action: &types.Action{}})

	require.Len(t, results, 1)
	require.NoError(t, results[0].Err)
	assert.Equal(t, []string{"first", "second", "third"}, order,
		"stages must execute in pipeline order")
}

func TestPipeline_StageErrorSkipsRemainingStages(t *testing.T) {
	sentinel := errors.New("stage failed")
	var ran []string
	p := NewPipeline(Hooks{},
		&fakeStage{name: "first", fn: func(context.Context, *WorkItem) error {
			ran = append(ran, "first")
			return sentinel
		}},
		&fakeStage{name: "second", fn: func(context.Context, *WorkItem) error {
			ran = append(ran, "second")
			return nil
		}},
	)

	results := runPipeline(p, &WorkItem{Action: &types.Action{}})

	require.Len(t, results, 1)
	require.ErrorIs(t, results[0].Err, sentinel)
	assert.Equal(t, "first", results[0].FailedStage)
	assert.Equal(t, []string{"first"}, ran,
		"stages after a failure must be skipped")
}

func TestPipeline_TerminatedItemSkipsRemainingStages(t *testing.T) {
	var ran []string
	p := NewPipeline(Hooks{},
		&fakeStage{name: "first", fn: func(_ context.Context, item *WorkItem) error {
			ran = append(ran, "first")
			item.Terminated = true
			return nil
		}},
		&fakeStage{name: "second", fn: func(context.Context, *WorkItem) error {
			ran = append(ran, "second")
			return nil
		}},
	)

	results := runPipeline(p, &WorkItem{Action: &types.Action{}})

	require.Len(t, results, 1)
	require.NoError(t, results[0].Err)
	assert.True(t, results[0].Terminated)
	assert.Equal(t, []string{"first"}, ran,
		"a terminated item must flow through without invoking later stages")
}

func TestPipeline_StageHooksWrapEveryStage(t *testing.T) {
	var events []string
	hooks := Hooks{
		BeforeStage: func(stage string, _ *types.Action) error {
			events = append(events, "before:"+stage)
			return nil
		},
		AfterStage: func(stage string, _ *types.Action) error {
			events = append(events, "after:"+stage)
			return nil
		},
	}
	p := NewPipeline(hooks,
		&fakeStage{name: "one"},
		&fakeStage{name: "two"},
	)

	results := runPipeline(p, &WorkItem{Action: &types.Action{}})

	require.Len(t, results, 1)
	require.NoError(t, results[0].Err)
	assert.Equal(t, []string{"before:one", "after:one", "before:two", "after:two"}, events)
}

func TestPipeline_BeforeStageErrorAbortsStage(t *testing.T) {
	sentinel := errors.New("aborted by BeforeStage")
	stageRan := false
	hooks := Hooks{
		BeforeStage: func(stage string, _ *types.Action) error {
			if stage == "guarded" {
				return sentinel
			}
			return nil
		},
	}
	p := NewPipeline(hooks,
		&fakeStage{name: "guarded", fn: func(context.Context, *WorkItem) error {
			stageRan = true
			return nil
		}},
	)

	results := runPipeline(p, &WorkItem{Action: &types.Action{}})

	require.Len(t, results, 1)
	require.ErrorIs(t, results[0].Err, sentinel)
	assert.Equal(t, "guarded", results[0].FailedStage)
	assert.False(t, stageRan, "BeforeStage error must prevent the stage from running")
}

func TestPipeline_AfterStageErrorRecordedAsItemError(t *testing.T) {
	sentinel := errors.New("post-stage failure")
	hooks := Hooks{
		AfterStage: func(string, *types.Action) error { return sentinel },
	}
	p := NewPipeline(hooks, &fakeStage{name: "one"})

	results := runPipeline(p, &WorkItem{Action: &types.Action{}})

	require.Len(t, results, 1)
	require.ErrorIs(t, results[0].Err, sentinel)
	assert.Equal(t, "one", results[0].FailedStage)
}

func TestPipeline_MultipleItemsPreserveOrder(t *testing.T) {
	p := NewPipeline(Hooks{}, &fakeStage{name: "only"})

	items := []*WorkItem{
		{Action: &types.Action{Input: "a"}},
		{Action: &types.Action{Input: "b"}},
		{Action: &types.Action{Input: "c"}},
	}
	results := runPipeline(p, items...)

	require.Len(t, results, 3)
	for i, item := range items {
		assert.Same(t, item, results[i], "items must come out in input order")
	}
}
