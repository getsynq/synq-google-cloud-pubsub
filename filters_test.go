package main

import (
	"testing"

	"github.com/getsynq/synq-google-cloud-pubsub/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestABadResourcePatternNamesItsSection is the reproducer for a typo in a
// config file arriving as a stack trace. A regex is user input; failing to
// compile one is a configuration error and has to name the key it came from.
func TestABadResourcePatternNamesItsSection(t *testing.T) {
	_, err := buildFilters(&config.Config{Filter: config.FilterConfig{
		Topics: config.FilterRules{Exclude: []string{"prod-("}},
	}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "filter.topics.exclude")
	assert.Contains(t, err.Error(), `prod-(`)
}

// TestEachFilterSectionNamesItself keeps the three sections distinct in the
// message; naming the wrong one sends the reader to a key that is fine.
func TestEachFilterSectionNamesItself(t *testing.T) {
	for section, cfg := range map[string]*config.Config{
		"filter.subscriptions.include": {Filter: config.FilterConfig{
			Subscriptions: config.FilterRules{Include: []string{"["}},
		}},
		"relationships.filter.exclude": {Relationships: config.RelationshipsConfig{
			Filter: config.FilterRules{Exclude: []string{"["}},
		}},
	} {
		_, err := buildFilters(cfg)
		require.Error(t, err, section)
		assert.Contains(t, err.Error(), section)
	}
}

func TestValidPatternsStillCompile(t *testing.T) {
	filters, err := buildFilters(&config.Config{Filter: config.FilterConfig{
		Topics:        config.FilterRules{Include: []string{"^prod\\."}},
		Subscriptions: config.FilterRules{Exclude: []string{"\\.dead-letter$"}},
	}})

	require.NoError(t, err)
	assert.True(t, filters.topics.Accept("prod.orders"))
	assert.False(t, filters.topics.Accept("staging.orders"))
	assert.False(t, filters.subscriptions.Accept("prod.orders.dead-letter"))
}
