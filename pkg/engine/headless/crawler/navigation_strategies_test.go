package crawler

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/projectdiscovery/katana/pkg/engine/headless/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStrategy struct {
	name    string
	hash    string
	apply   bool
	err     error
	visited *[]string
}

func (f fakeStrategy) Name() string { return f.name }

func (f fakeStrategy) Restore(_ *NavigationContext) (NavigationResult, error) {
	if f.visited != nil {
		*f.visited = append(*f.visited, f.name)
	}
	if f.err != nil {
		return NavigationResult{}, f.err
	}
	if !f.apply {
		return NavigationResult{}, nil
	}
	return NavigationResult{PageHash: f.hash}, nil
}

func TestNavigationStrategyChain_FirstApplicableWins(t *testing.T) {
	var visited []string
	chain := NewNavigationStrategyChain(slog.Default(),
		fakeStrategy{name: "skip", apply: false, visited: &visited},
		fakeStrategy{name: "win", hash: "hash-1", apply: true, visited: &visited},
		fakeStrategy{name: "never", hash: "hash-2", apply: true, visited: &visited},
	)

	hash, err := chain.Restore(&NavigationContext{})
	require.NoError(t, err)
	assert.Equal(t, "hash-1", hash)
	assert.Equal(t, []string{"skip", "win"}, visited, "later strategies must not run after a win")
}

func TestNavigationStrategyChain_EarlyFailureFallsThrough(t *testing.T) {
	var visited []string
	chain := NewNavigationStrategyChain(slog.Default(),
		fakeStrategy{name: "fails", err: errors.New("boom"), visited: &visited},
		fakeStrategy{name: "win", hash: "hash-2", apply: true, visited: &visited},
	)

	hash, err := chain.Restore(&NavigationContext{})
	require.NoError(t, err)
	assert.Equal(t, "hash-2", hash)
}

func TestNavigationStrategyChain_FinalFailurePropagates(t *testing.T) {
	sentinel := errors.New("terminal")
	chain := NewNavigationStrategyChain(slog.Default(),
		fakeStrategy{name: "fails-a", err: errors.New("a")},
		fakeStrategy{name: "fails-b", err: sentinel},
	)

	_, err := chain.Restore(&NavigationContext{})
	require.ErrorIs(t, err, sentinel)
}

func TestNavigationStrategyChain_NoneApplicableReturnsNoNavigationPossible(t *testing.T) {
	chain := NewNavigationStrategyChain(slog.Default(),
		fakeStrategy{name: "a", apply: false},
		fakeStrategy{name: "b", apply: false},
	)

	_, err := chain.Restore(&NavigationContext{})
	require.ErrorIs(t, err, ErrNoNavigationPossible)
}

func TestNavigationStrategyChain_EmptyChainReturnsNoNavigationPossible(t *testing.T) {
	chain := NewNavigationStrategyChain(slog.Default())
	_, err := chain.Restore(&NavigationContext{})
	require.ErrorIs(t, err, ErrNoNavigationPossible)
}

func TestDefaultNavigationStrategies_OrderAndTypes(t *testing.T) {
	c := &Crawler{logger: slog.Default()}
	strategies := defaultNavigationStrategies(c)

	require.Len(t, strategies, 3)
	assert.Equal(t, "element-visibility", strategies[0].Name())
	assert.Equal(t, "browser-history", strategies[1].Name())
	assert.Equal(t, "shortest-path", strategies[2].Name())
}

func TestNew_RegistersDefaultStrategyChain(t *testing.T) {
	c, err := New(Options{})
	require.NoError(t, err)
	defer c.Close()

	strategies := c.NavigationStrategies()
	require.Len(t, strategies, 3)
	assert.Equal(t, "element-visibility", strategies[0].Name())
	assert.Equal(t, "browser-history", strategies[1].Name())
	assert.Equal(t, "shortest-path", strategies[2].Name())
}

func TestSetNavigationStrategies_ReplacesAndRestores(t *testing.T) {
	c, err := New(Options{})
	require.NoError(t, err)
	defer c.Close()

	custom := []NavigationStrategy{fakeStrategy{name: "custom", apply: false}}
	c.SetNavigationStrategies(custom)
	assert.Len(t, c.NavigationStrategies(), 1)
	assert.Equal(t, "custom", c.NavigationStrategies()[0].Name())

	c.SetNavigationStrategies(nil)
	assert.Len(t, c.NavigationStrategies(), 3, "nil restores the default chain")
}

func TestNew_HonorsCustomNavigationStrategies(t *testing.T) {
	c, err := New(Options{NavigationStrategies: []NavigationStrategy{
		fakeStrategy{name: "only", hash: "h", apply: true},
	}})
	require.NoError(t, err)
	defer c.Close()

	strategies := c.NavigationStrategies()
	require.Len(t, strategies, 1)
	assert.Equal(t, "only", strategies[0].Name())
}

func TestIsElementMatch(t *testing.T) {
	t.Run("identical ids match", func(t *testing.T) {
		assert.True(t, isElementMatch(
			&types.HTMLElement{ID: "x"},
			&types.HTMLElement{ID: "x"},
		))
	})

	t.Run("nil never matches", func(t *testing.T) {
		assert.False(t, isElementMatch(nil, &types.HTMLElement{ID: "x"}))
		assert.False(t, isElementMatch(&types.HTMLElement{ID: "x"}, nil))
	})

	t.Run("two matching attributes match", func(t *testing.T) {
		cur := &types.HTMLElement{Classes: "a", TextContent: "t", TagName: "div"}
		tgt := &types.HTMLElement{Classes: "a", TextContent: "t", TagName: "span"}
		assert.True(t, isElementMatch(cur, tgt))
	})

	t.Run("single matching attribute does not match", func(t *testing.T) {
		cur := &types.HTMLElement{Classes: "a"}
		tgt := &types.HTMLElement{Classes: "a"}
		assert.False(t, isElementMatch(cur, tgt))
	})

	t.Run("empty fields are not counted", func(t *testing.T) {
		assert.False(t, isElementMatch(&types.HTMLElement{}, &types.HTMLElement{}))
	})
}
