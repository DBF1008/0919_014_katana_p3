package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testItem struct {
	skip   bool
	trace  []string
	failAt string
}

func (i *testItem) Skipped() bool  { return i.skip }
func (i *testItem) SkipRemaining() { i.skip = true }

func traceStage(name StageName, trace *[]string, mu *sync.Mutex) StageFunc[*testItem] {
	return StageFunc[*testItem]{
		StageName: name,
		Fn: func(_ context.Context, item *testItem) error {
			mu.Lock()
			*trace = append(*trace, string(name))
			item.trace = append(item.trace, string(name))
			mu.Unlock()
			return nil
		},
	}
}

func runPipe(t *testing.T, ctx context.Context, stages []Stage[*testItem], item *testItem) (*testItem, *ItemError[*testItem]) {
	t.Helper()
	p := New(stages...)
	p.Run(ctx)

	select {
	case p.Input() <- item:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out sending item")
	}

	select {
	case out := <-p.Success():
		p.Shutdown()
		return out, nil
	case fail := <-p.Errors():
		p.Shutdown()
		return nil, fail
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pipeline result")
		return nil, nil
	}
}

func TestPipeline_ItemFlowsThroughAllStagesInOrder(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	stages := []Stage[*testItem]{
		traceStage("a", &trace, &mu),
		traceStage("b", &trace, &mu),
		traceStage("c", &trace, &mu),
	}

	out, fail := runPipe(t, context.Background(), stages, &testItem{})

	require.Nil(t, fail)
	require.NotNil(t, out)
	assert.Equal(t, []string{"a", "b", "c"}, out.trace)
}

func TestPipeline_StageErrorRoutesToErrorsAndStopsLaterStages(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	sentinel := errors.New("boom")
	stages := []Stage[*testItem]{
		traceStage("a", &trace, &mu),
		StageFunc[*testItem]{
			StageName: "b",
			Fn: func(_ context.Context, item *testItem) error {
				mu.Lock()
				trace = append(trace, "b")
				mu.Unlock()
				return sentinel
			},
		},
		traceStage("c", &trace, &mu),
	}

	out, fail := runPipe(t, context.Background(), stages, &testItem{})

	require.Nil(t, out)
	require.NotNil(t, fail)
	assert.ErrorIs(t, fail.Err, sentinel)
	assert.Equal(t, StageName("b"), fail.Stage)
	mu.Lock()
	assert.Equal(t, []string{"a", "b"}, trace, "stage c must never run after b errors")
	mu.Unlock()
}

func TestPipeline_SkipBypassesRemainingStages(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	stages := []Stage[*testItem]{
		traceStage("a", &trace, &mu),
		StageFunc[*testItem]{
			StageName: "b",
			Fn: func(_ context.Context, item *testItem) error {
				mu.Lock()
				trace = append(trace, "b")
				mu.Unlock()
				item.SkipRemaining()
				return nil
			},
		},
		traceStage("c", &trace, &mu),
	}

	out, fail := runPipe(t, context.Background(), stages, &testItem{})

	require.Nil(t, fail)
	require.NotNil(t, out)
	assert.True(t, out.Skipped())
	mu.Lock()
	assert.Equal(t, []string{"a", "b"}, trace, "skipped item bypasses stage c")
	mu.Unlock()
}

func TestPipeline_PreMarkedSkipBypassesAllStages(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	stages := []Stage[*testItem]{
		traceStage("a", &trace, &mu),
		traceStage("b", &trace, &mu),
	}

	out, fail := runPipe(t, context.Background(), stages, &testItem{skip: true})

	require.Nil(t, fail)
	require.NotNil(t, out)
	mu.Lock()
	assert.Empty(t, trace, "no stage Process runs for an item already marked skipped")
	mu.Unlock()
}

func TestPipeline_MultipleItemsProcessedSequentially(t *testing.T) {
	stages := []Stage[*testItem]{
		StageFunc[*testItem]{
			StageName: "a",
			Fn: func(_ context.Context, item *testItem) error {
				item.trace = append(item.trace, "seen")
				return nil
			},
		},
	}
	p := New(stages...)
	p.Run(context.Background())

	const n = 5
	for i := 0; i < n; i++ {
		p.Input() <- &testItem{}
	}

	successes, failures := 0, 0
	for successes+failures < n {
		select {
		case <-p.Success():
			successes++
		case <-p.Errors():
			failures++
		case <-time.After(2 * time.Second):
			t.Fatal("timed out draining pipeline")
		}
	}
	p.Shutdown()

	assert.Equal(t, n, successes)
	assert.Zero(t, failures)
}

func TestPipeline_ContextCancelUnblocksInflightItem(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	release := make(chan struct{})
	stages := []Stage[*testItem]{
		StageFunc[*testItem]{
			StageName: "blocking",
			Fn: func(_ context.Context, item *testItem) error {
				close(started)
				<-release
				return nil
			},
		},
	}
	p := New(stages...)
	p.Run(ctx)

	p.Input() <- &testItem{}
	<-started

	// The stage itself is blocked, but an owner waiting on Success/Errors
	// must be able to abandon the item by cancelling ctx.
	done := make(chan struct{})
	go func() {
		select {
		case <-p.Success():
		case <-p.Errors():
		case <-ctx.Done():
		}
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("context cancellation did not unblock the pipeline consumer")
	}
	close(release)
	p.Shutdown()
}

func TestPipeline_NoStagesForwardsToSuccess(t *testing.T) {
	p := New[*testItem]()
	p.Run(context.Background())

	p.Input() <- &testItem{}
	select {
	case out := <-p.Success():
		assert.NotNil(t, out)
	case <-time.After(2 * time.Second):
		t.Fatal("empty pipeline did not forward item")
	}
	close(p.Input())
	p.Wait()
}
