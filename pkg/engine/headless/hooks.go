package headless

import "github.com/projectdiscovery/katana/pkg/engine/headless/crawler"

// Hooks re-exports crawler.Hooks so library users can configure headless
// lifecycle callbacks without importing the internal crawler sub-package.
// See crawler.Hooks for field-level documentation and semantics.
type Hooks = crawler.Hooks

// StageName re-exports the pipeline stage identifier used by BeforeStage and
// AfterStage callbacks.
type StageName = crawler.StageName

// Pipeline stage names re-exported for use in BeforeStage/AfterStage hooks.
const (
	StageActionProcessor    = crawler.StageActionProcessor
	StageNavigator          = crawler.StageNavigator
	StageCaptchaHandler     = crawler.StageCaptchaHandler
	StageAuthHandler        = crawler.StageAuthHandler
	StageDiscoveryCollector = crawler.StageDiscoveryCollector
	StageGraphWriter        = crawler.StageGraphWriter
)

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
