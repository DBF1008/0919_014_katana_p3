package crawler

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/utils"
	"github.com/stretchr/testify/assert"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestClassifyFailureKind(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want failureKind
	}{
		{"element not visible", ErrElementNotVisible, kindNotVisible},
		{"no pointer events", &rod.NoPointerEventsError{}, kindNotVisible},
		{"invisible shape", &rod.InvisibleShapeError{}, kindNotVisible},
		{"navigation error", &rod.NavigationError{}, kindNavigationFailed},
		{"no navigation possible", ErrNoNavigationPossible, kindNoNavigation},
		{"max sleep count", &utils.MaxSleepCountError{}, kindTimeout},
		{"generic site error", io.EOF, kindSiteSpecific},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, classifyFailureKind(tt.err))
		})
	}
}

func TestClassifyPipelineError(t *testing.T) {
	logger := discardLogger()
	actionName := "left_click"

	tests := []struct {
		name string
		err  error
		want errorAction
	}{
		{"no crawling action stops crawl", ErrNoCrawlingAction, errorStopCrawl},
		{"wrapped no crawling action stops crawl", io.EOF, errorContinue},
		{"element not visible continues", ErrElementNotVisible, errorContinue},
		{"no pointer events continues", &rod.NoPointerEventsError{}, errorContinue},
		{"invisible shape continues", &rod.InvisibleShapeError{}, errorContinue},
		{"navigation error continues", &rod.NavigationError{}, errorContinue},
		{"no navigation possible continues", ErrNoNavigationPossible, errorContinue},
		{"max sleep count continues", &utils.MaxSleepCountError{}, errorContinue},
		{"generic site error continues", errors.New("boom"), errorContinue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, classifyPipelineError(logger, actionName, tt.err))
		})
	}
}
