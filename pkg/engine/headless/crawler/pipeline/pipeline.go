// Package pipeline implements the stage-based processing pipeline used by the
// headless crawler.
//
// A Pipeline is an ordered list of Stages. Every item handed to the
// pipeline's input channel flows through the stages in registration order.
// Stages are connected with channels. Each stage runs a processor goroutine
// (consumes its input, calls Process, emits a result) and a forwarder
// goroutine (routes the result to the next stage or to the terminal fan-in),
// so a blocked downstream stage can never prevent an upstream stage from
// consuming its own input.
//
// A stage has two ways of short-circuiting an item:
//
//   - Returning an error sends the item to the pipeline's errors channel; no
//     subsequent stage runs for that item.
//   - Marking the item skipped (via the Skippable constraint) forwards it
//     straight to the output, bypassing the remaining stages.
//
// Both output and errors are fan-in channels consumed by the orchestrator.
//
// The type parameter T is the item type carried between stages. It is usually
// a pointer type (for example *crawlItem) so stages mutate a shared envelope;
// T itself must satisfy Skippable.
package pipeline

import (
	"context"
	"sync"
)

// StageName is the lifecycle identifier of a pipeline stage. It is reported to
// stage hooks so a single callback can distinguish stages.
type StageName string

// Skippable is implemented by item envelopes that can bypass downstream
// stages. Skipped reports whether an earlier stage marked the item;
// SkipRemaining sets the mark.
type Skippable interface {
	Skipped() bool
	SkipRemaining()
}

// Stage processes a single item of type T. Stages must not retain references
// to an item after Process returns: ownership belongs to the pipeline while
// an item is in flight.
//
// Returning an error aborts processing of the item for every later stage and
// delivers it to the errors channel. Calling item.SkipRemaining() forwards the
// (possibly mutated) item to the output without running later stages.
type Stage[T Skippable] interface {
	// Name uniquely identifies the stage for lifecycle hooks and logging.
	Name() StageName
	// Process mutates the item in place and returns nil on success.
	Process(ctx context.Context, item T) error
}

// StageFunc adapts a plain function (plus a name) into a Stage.
type StageFunc[T Skippable] struct {
	StageName StageName
	Fn        func(ctx context.Context, item T) error
}

// Name implements Stage.
func (s StageFunc[T]) Name() StageName { return s.StageName }

// Process implements Stage.
func (s StageFunc[T]) Process(ctx context.Context, item T) error {
	return s.Fn(ctx, item)
}

// ItemError pairs an item with the error that aborted its processing.
type ItemError[T any] struct {
	// Item is the item as it was when the error occurred. It may carry
	// partial results produced by earlier stages.
	Item T
	// Stage is the stage that produced the error.
	Stage StageName
	Err   error
}

// terminal is the internal envelope emitted by a stage processor: either a
// successful item or a stage error.
type terminal[T any] struct {
	item T
	fail *ItemError[T]
}

// Pipeline is an ordered, channel-connected chain of stages.
type Pipeline[T Skippable] struct {
	stages []Stage[T]

	input   chan T
	success chan T
	failed  chan *ItemError[T]

	// terminal collects every final envelope from the stage forwarders; a
	// splitter routes them to Success or Errors.
	terminal chan *terminal[T]

	// ctxDone is closed when the run context is cancelled. Forwarders use it
	// to abandon blocked downstream sends.
	ctxDone <-chan struct{}

	// inFlight counts stage forwarders + the feeder. Every accepted item is
	// owned by exactly one of these while in flight.
	inFlight sync.WaitGroup
	// splitterWG tracks the terminal splitter.
	splitterWG sync.WaitGroup
}

// New assembles a pipeline from the given ordered stages and wires the
// channels between them.
func New[T Skippable](stages ...Stage[T]) *Pipeline[T] {
	return &Pipeline[T]{
		stages:   stages,
		input:    make(chan T),
		success:  make(chan T),
		failed:   make(chan *ItemError[T]),
		terminal: make(chan *terminal[T]),
	}
}

// Stages returns the ordered stages registered on the pipeline.
func (p *Pipeline[T]) Stages() []Stage[T] { return p.stages }

// Input returns the channel on which the owner enqueues items.
func (p *Pipeline[T]) Input() chan<- T { return p.input }

// Success returns the channel on which fully processed (or skipped) items
// arrive.
func (p *Pipeline[T]) Success() <-chan T { return p.success }

// Errors returns the channel on which failed items arrive.
func (p *Pipeline[T]) Errors() <-chan *ItemError[T] { return p.failed }

// Run starts every stage goroutine. It returns immediately; consume Success
// and Errors while feeding Input, then call Shutdown. Run must only be
// invoked once.
//
// ctx is passed to stage Process calls and unblocks stage-to-stage forwarding
// when the owner abandons the crawl: on cancellation an in-flight item is
// rerouted to Errors instead of waiting on a downstream channel.
func (p *Pipeline[T]) Run(ctx context.Context) {
	p.ctxDone = ctx.Done()

	if len(p.stages) == 0 {
		p.runPassthrough()
		p.startSplitter()
		return
	}

	const inFlightBuffer = 1
	stageIn := make([]chan T, len(p.stages))
	stageOut := make([]chan *terminal[T], len(p.stages))
	for i := range p.stages {
		stageIn[i] = make(chan T, inFlightBuffer)
		stageOut[i] = make(chan *terminal[T], inFlightBuffer)
	}

	for i, stage := range p.stages {
		// Processor: consumes own input, invokes Process, emits one terminal
		// envelope per item on own output.
		p.inFlight.Add(1)
		go func(in <-chan T, out chan<- *terminal[T], st Stage[T]) {
			defer p.inFlight.Done()
			for item := range in {
				if item.Skipped() {
					out <- &terminal[T]{item: item}
					continue
				}
				if procErr := st.Process(ctx, item); procErr != nil {
					out <- &terminal[T]{fail: &ItemError[T]{Item: item, Stage: st.Name(), Err: procErr}}
					continue
				}
				out <- &terminal[T]{item: item}
			}
			close(out)
		}(stageIn[i], stageOut[i], stage)

		// Forwarder: routes own terminal envelopes to the next stage's input
		// or to the terminal fan-in. Separate from the processor so a blocked
		// send cannot stall input consumption.
		p.inFlight.Add(1)
		go func(out <-chan *terminal[T], nextIn chan T, st Stage[T], isLast bool) {
			defer p.inFlight.Done()
			for res := range out {
				if res.fail != nil || isLast {
					p.forwardTerminal(res)
					continue
				}
				select {
				case nextIn <- res.item:
				case <-p.ctxDone:
					p.forwardTerminal(&terminal[T]{
						fail: &ItemError[T]{Item: res.item, Stage: st.Name(), Err: context.Cause(ctx)},
					})
				}
			}
			// Our processor is done: close the next stage's input once any
			// late in-flight items have been drained. On cancellation there
			// may be nobody left to consume those items, so the drain waits on
			// ctxDone too.
			if nextIn != nil {
				p.drainAndClose(nextIn)
			}
		}(stageOut[i], nextInput(stageIn, i), stage, i == len(p.stages)-1)
	}

	// Feeder: owner input -> first stage; on cancellation the item becomes a
	// terminal error.
	p.inFlight.Add(1)
	go func() {
		defer p.inFlight.Done()
		for item := range p.input {
			select {
			case stageIn[0] <- item:
			case <-p.ctxDone:
				p.forwardTerminal(&terminal[T]{fail: &ItemError[T]{Item: item, Err: context.Cause(ctx)}})
			}
		}
		close(stageIn[0])
	}()

	p.startSplitter()
}

// startSplitter closes the terminal channel once every forwarder/feeder is
// done and routes the final envelopes to Success/Errors.
func (p *Pipeline[T]) startSplitter() {
	go func() {
		p.inFlight.Wait()
		close(p.terminal)
	}()

	p.splitterWG.Add(1)
	go func() {
		defer p.splitterWG.Done()
		defer close(p.success)
		defer close(p.failed)
		for res := range p.terminal {
			if res.fail != nil {
				select {
				case p.failed <- res.fail:
				case <-p.ctxDone:
				}
			} else {
				select {
				case p.success <- res.item:
				case <-p.ctxDone:
				}
			}
		}
	}()
}

// forwardTerminal emits an envelope to the terminal fan-in, dropping it after
// context cancellation (the owner has stopped draining).
func (p *Pipeline[T]) forwardTerminal(res *terminal[T]) {
	select {
	case p.terminal <- res:
	case <-p.ctxDone:
	}
}

// drainAndClose closes ch, first non-blockingly routing any queued items to
// the terminal fan-in as cancellation errors. It unblocks the next processor
// once this stage has terminated.
func (p *Pipeline[T]) drainAndClose(ch chan T) {
	defer close(ch)
	for {
		select {
		case item := <-ch:
			p.forwardTerminal(&terminal[T]{fail: &ItemError[T]{Item: item, Err: context.Canceled}})
		default:
			return
		}
	}
}

// nextInput returns stage i+1's input channel, or nil for the last stage.
func nextInput[T any](stageIn []chan T, i int) chan T {
	if i+1 < len(stageIn) {
		return stageIn[i+1]
	}
	return nil
}

// runPassthrough backs a stage-less pipeline: each input is forwarded to the
// terminal channel directly.
func (p *Pipeline[T]) runPassthrough() {
	p.inFlight.Add(1)
	go func() {
		defer p.inFlight.Done()
		for item := range p.input {
			p.forwardTerminal(&terminal[T]{item: item})
		}
	}()
}

// Wait blocks until the splitter has terminated (all accepted items reached
// Success/Errors and those channels were closed and drained).
func (p *Pipeline[T]) Wait() { p.splitterWG.Wait() }

// Shutdown gracefully stops the pipeline: it stops accepting input and drains
// Success/Errors until every accepted item has terminated, then returns. After
// Shutdown the ctx passed to Run may be cancelled safely.
func (p *Pipeline[T]) Shutdown() {
	close(p.input)

	successCh, failedCh := p.success, p.failed
	for successCh != nil || failedCh != nil {
		select {
		case _, ok := <-successCh:
			if !ok {
				successCh = nil
			}
		case _, ok := <-failedCh:
			if !ok {
				failedCh = nil
			}
		}
	}
	p.splitterWG.Wait()
}
