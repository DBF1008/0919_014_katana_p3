package crawler

import (
	"log/slog"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/utils"
	"github.com/pkg/errors"
)

// errorAction tells the crawl orchestrator how to react to a pipeline failure.
type errorAction int

const (
	// errorStopCrawl aborts the whole crawl. Only used when the queue is
	// exhausted (ErrNoCrawlingAction).
	errorStopCrawl errorAction = iota
	// errorContinue drops the failed action, counts a consecutive failure and
	// moves on to the next queued action.
	errorContinue
)

// failureKind categorizes an action failure for logging. The categorization
// itself never calls err.Error(): some rod error types dereference browser
// state in their Error() implementation and cannot be stringified without a
// live page.
type failureKind string

const (
	kindNotVisible       failureKind = "not_visible"
	kindNavigationFailed failureKind = "navigation_failed"
	kindNoNavigation     failureKind = "no_navigation_possible"
	kindTimeout          failureKind = "timeout"
	kindSiteSpecific     failureKind = "site_specific"
)

// classifyFailureKind maps an error to a log category.
func classifyFailureKind(err error) failureKind {
	switch {
	case errors.Is(err, ErrElementNotVisible):
		return kindNotVisible
	default:
		var npe *rod.NoPointerEventsError
		var ish *rod.InvisibleShapeError
		if errors.As(err, &npe) || errors.As(err, &ish) {
			return kindNotVisible
		}

		var ne *rod.NavigationError
		if errors.As(err, &ne) {
			return kindNavigationFailed
		}

		if errors.Is(err, ErrNoNavigationPossible) {
			return kindNoNavigation
		}

		var msce *utils.MaxSleepCountError
		if errors.As(err, &msce) {
			return kindTimeout
		}

		return kindSiteSpecific
	}
}

// classifyPipelineError maps a stage error onto an orchestrator action. It is
// the single home of the crawler's error taxonomy: every failure either
// terminates the crawl or is counted and skipped.
func classifyPipelineError(logger *slog.Logger, actionName string, err error) errorAction {
	if errors.Is(err, ErrNoCrawlingAction) {
		return errorStopCrawl
	}

	switch classifyFailureKind(err) {
	case kindNotVisible:
		logger.Debug("Skipping action as it is not visible",
			slog.String("action", actionName),
		)
	case kindNavigationFailed:
		logger.Debug("Skipping action as navigation failed",
			slog.String("action", actionName),
		)
	case kindNoNavigation:
		logger.Debug("Skipping action as no navigation possible", slog.String("action", actionName))
	case kindTimeout:
		logger.Debug("Skipping action as it is taking too long", slog.String("action", actionName))
	case kindSiteSpecific:
		logger.Debug("Skipping action due to site-specific error",
			slog.String("error", err.Error()),
			slog.String("action", actionName),
		)
	}

	return errorContinue
}
