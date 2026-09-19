package crawler

import (
	"context"
	"sync"

	"github.com/projectdiscovery/katana/pkg/engine/headless/browser"
	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
)

// WorkItem carries a single crawl action and its associated browser page
// through the pipeline stages. Stages may attach intermediate results
// (page hash, page state, discovered navigations) to the item so later
// stages can consume them without recomputation.
type WorkItem struct {
	Action *types.Action
	Page   *browser.BrowserPage

	// CurrentPageHash is the hash of the page the action is executed on,
	// computed by the navigator stage after state restoration.
	CurrentPageHash string
	// PageState is the post-action page state, built by the discovery stage.
	PageState *types.PageState
	// Navigations are the actions discovered by the discovery stage.
	Navigations []*types.Action

	// Terminated asks the pipeline to skip all remaining stages for this
	// item without treating it as an error (e.g. captcha pages, out-of-scope
	// pages).
	Terminated bool
	// Err is the first error reported by a stage. Once set, all remaining
	// stages are skipped and the item is forwarded as-is.
	Err error
	// FailedStage is the name of the stage that set Err.
	FailedStage string
}

// Stage is a single step of the crawl pipeline. A stage processes a WorkItem
// and either returns an error (which halts the pipeline for this item) or
// mutates the item for downstream stages.
type Stage interface {
	// Name identifies the stage for logging, diagnostics and hooks.
	Name() string
	// Process handles a single work item.
	Process(ctx context.Context, item *WorkItem) error
}

// Pipeline connects an ordered list of stages with channels. Each stage runs
// in its own goroutine; items read from the input channel flow through every
// stage in order and are emitted on the output channel once the last stage
// (or an early termination) is reached.
//
// The pipeline preserves ordering: an item is forwarded downstream only after
// the previous item has been forwarded by the same stage. Callers that feed
// one item and wait for its result before feeding the next get strictly
// sequential stage execution for shared crawler state.
type Pipeline struct {
	stages []Stage
	hooks  Hooks
	wg     sync.WaitGroup
}

// NewPipeline builds a pipeline from the given stages, consulted in order.
// The supplied hooks are invoked around every stage execution; see
// Hooks.BeforeStage and Hooks.AfterStage for semantics.
func NewPipeline(hooks Hooks, stages ...Stage) *Pipeline {
	return &Pipeline{stages: stages, hooks: hooks}
}

// Run starts one goroutine per stage, each connected to the next by an
// unbuffered channel, and returns the output channel of the last stage.
// The output channel is closed once `in` is closed and fully drained, or
// after ctx is cancelled. Callers should invoke Wait to ensure all stage
// goroutines have exited.
func (p *Pipeline) Run(ctx context.Context, in <-chan *WorkItem) <-chan *WorkItem {
	out := in
	for _, stage := range p.stages {
		next := make(chan *WorkItem)
		p.wg.Add(1)
		go p.runStage(ctx, stage, out, next)
		out = next
	}
	return out
}

// Wait blocks until all stage goroutines started by Run have exited.
func (p *Pipeline) Wait() {
	p.wg.Wait()
}

func (p *Pipeline) runStage(ctx context.Context, stage Stage, in <-chan *WorkItem, out chan<- *WorkItem) {
	defer p.wg.Done()
	defer close(out)
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-in:
			if !ok {
				return
			}
			p.process(ctx, stage, item)
			select {
			case out <- item:
			case <-ctx.Done():
				return
			}
		}
	}
}

// process runs a single stage for an item, honouring early termination and
// invoking the pipeline lifecycle hooks. Once an item carries an error or is
// terminated, remaining stages are skipped and the item flows through
// unchanged.
func (p *Pipeline) process(ctx context.Context, stage Stage, item *WorkItem) {
	if item.Err != nil || item.Terminated {
		return
	}
	if cb := p.hooks.BeforeStage; cb != nil {
		if err := cb(stage.Name(), item.Action); err != nil {
			item.Err = err
			item.FailedStage = stage.Name()
			return
		}
	}
	if err := stage.Process(ctx, item); err != nil {
		item.Err = err
		item.FailedStage = stage.Name()
		return
	}
	if item.Terminated {
		return
	}
	if cb := p.hooks.AfterStage; cb != nil {
		if err := cb(stage.Name(), item.Action); err != nil {
			item.Err = err
			item.FailedStage = stage.Name()
		}
	}
}
