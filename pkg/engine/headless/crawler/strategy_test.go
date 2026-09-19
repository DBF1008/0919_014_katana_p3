package crawler

import (
	"errors"
	"testing"

	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStrategy records its invocation and returns a canned result.
type fakeStrategy struct {
	name   string
	hash   string
	err    error
	called *[]string
}

func (f *fakeStrategy) Name() string { return f.name }

func (f *fakeStrategy) Navigate(_ *NavigationContext) (string, error) {
	*f.called = append(*f.called, f.name)
	return f.hash, f.err
}

func newNavContext() *NavigationContext {
	return &NavigationContext{
		Action:          &types.Action{OriginID: "origin"},
		CurrentPageHash: "current",
		OriginPageState: &types.PageState{UniqueID: "origin"},
	}
}

func TestNavigationChain_FirstSuccessWins(t *testing.T) {
	var calls []string
	chain := NewNavigationChain(nil,
		&fakeStrategy{name: "one", hash: "hash-1", called: &calls},
		&fakeStrategy{name: "two", hash: "hash-2", called: &calls},
	)

	hash, err := chain.Navigate(newNavContext())

	require.NoError(t, err)
	assert.Equal(t, "hash-1", hash)
	assert.Equal(t, []string{"one"}, calls,
		"later strategies must not run once an earlier one succeeded")
}

func TestNavigationChain_FallsThroughWhenNotApplicable(t *testing.T) {
	var calls []string
	chain := NewNavigationChain(nil,
		&fakeStrategy{name: "one", hash: "", called: &calls},
		&fakeStrategy{name: "two", hash: "hash-2", called: &calls},
	)

	hash, err := chain.Navigate(newNavContext())

	require.NoError(t, err)
	assert.Equal(t, "hash-2", hash)
	assert.Equal(t, []string{"one", "two"}, calls)
}

func TestNavigationChain_NonTerminalErrorFallsThrough(t *testing.T) {
	var calls []string
	chain := NewNavigationChain(nil,
		&fakeStrategy{name: "one", err: errors.New("boom"), called: &calls},
		&fakeStrategy{name: "two", hash: "hash-2", called: &calls},
	)

	hash, err := chain.Navigate(newNavContext())

	require.NoError(t, err)
	assert.Equal(t, "hash-2", hash,
		"a non-terminal strategy error must not abort the chain")
	assert.Equal(t, []string{"one", "two"}, calls)
}

func TestNavigationChain_TerminalErrorPropagates(t *testing.T) {
	sentinel := errors.New("terminal failure")
	var calls []string
	chain := NewNavigationChain(nil,
		&fakeStrategy{name: "one", hash: "", called: &calls},
		&fakeStrategy{name: "two", err: sentinel, called: &calls},
	)

	hash, err := chain.Navigate(newNavContext())

	require.ErrorIs(t, err, sentinel)
	assert.Empty(t, hash)
}

func TestNavigationChain_NoStrategySucceeds(t *testing.T) {
	var calls []string
	chain := NewNavigationChain(nil,
		&fakeStrategy{name: "one", called: &calls},
		&fakeStrategy{name: "two", called: &calls},
	)

	hash, err := chain.Navigate(newNavContext())

	require.ErrorIs(t, err, ErrNoNavigationPossible)
	assert.Empty(t, hash)
	assert.Equal(t, []string{"one", "two"}, calls)
}

func TestDefaultNavigationChain_PriorityOrder(t *testing.T) {
	c := &Crawler{}
	chain := defaultNavigationChain(c)

	require.Len(t, chain.strategies, 3)
	assert.Equal(t, "element-visibility", chain.strategies[0].Name())
	assert.Equal(t, "browser-history", chain.strategies[1].Name())
	assert.Equal(t, "shortest-path", chain.strategies[2].Name())
}

func TestElementVisibilityStrategy_NotApplicable(t *testing.T) {
	strategy := &ElementVisibilityStrategy{c: &Crawler{}}

	t.Run("no element", func(t *testing.T) {
		hash, err := strategy.Navigate(&NavigationContext{
			Action:          &types.Action{},
			CurrentPageHash: "some-hash",
		})
		require.NoError(t, err)
		assert.Empty(t, hash, "strategy must defer to the chain when the action has no element")
	})

	t.Run("empty current page", func(t *testing.T) {
		hash, err := strategy.Navigate(&NavigationContext{
			Action:          &types.Action{Element: &types.HTMLElement{XPath: "/html/body/a"}},
			CurrentPageHash: emptyPageHash,
		})
		require.NoError(t, err)
		assert.Empty(t, hash, "strategy must defer to the chain on the blank page")
	})
}

func TestIsElementMatch(t *testing.T) {
	tests := []struct {
		name    string
		current *types.HTMLElement
		target  *types.HTMLElement
		match   bool
	}{
		{
			name:    "nil elements",
			current: nil,
			target:  &types.HTMLElement{},
			match:   false,
		},
		{
			name:    "identical IDs are definitive",
			current: &types.HTMLElement{ID: "submit"},
			target:  &types.HTMLElement{ID: "submit"},
			match:   true,
		},
		{
			name:    "different IDs with no other attributes",
			current: &types.HTMLElement{ID: "a"},
			target:  &types.HTMLElement{ID: "b"},
			match:   false,
		},
		{
			name:    "two matching attributes required",
			current: &types.HTMLElement{TagName: "a", TextContent: "Click"},
			target:  &types.HTMLElement{TagName: "a", TextContent: "Click"},
			match:   true,
		},
		{
			name:    "single matching attribute is not enough",
			current: &types.HTMLElement{TagName: "a", TextContent: "One"},
			target:  &types.HTMLElement{TagName: "a", TextContent: "Two"},
			match:   false,
		},
		{
			name:    "empty attributes do not count",
			current: &types.HTMLElement{TagName: "a"},
			target:  &types.HTMLElement{TagName: "a"},
			match:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.match, isElementMatch(tt.current, tt.target))
		})
	}
}
