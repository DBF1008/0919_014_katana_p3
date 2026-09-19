package headless

import "github.com/projectdiscovery/katana/pkg/engine/headless/crawler"

// Hooks re-exports crawler.Hooks so library users can configure headless
// lifecycle callbacks without importing the internal crawler sub-package.
// The callbacks align with the crawler's pipeline lifecycle: stage-level
// callbacks (BeforeStage/AfterStage) bracket each pipeline stage, while
// action-level (BeforeAction/AfterAction) and navigation-level
// (BeforeNavigateBack) callbacks fire inside the corresponding stages.
// See crawler.Hooks for field-level documentation and semantics.
type Hooks = crawler.Hooks

// SetHooks installs lifecycle callbacks on the headless engine. The supplied
// struct is copied, so mutating it after SetHooks returns has no effect on the
// engine; call SetHooks again to change the installed hooks. Passing nil clears
// any previously installed hooks. SetHooks is not safe to call concurrently
// with Crawl on the same engine.
func (h *Headless) SetHooks(hooks *Hooks) {
	if hooks == nil {
		h.hooks = Hooks{}
		return
	}
	h.hooks = *hooks
}
