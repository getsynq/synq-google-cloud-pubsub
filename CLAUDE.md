# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

**For general information about the project, configuration, and usage, see [README.md](README.md).**

## Development Commands

For basic commands (build, run, test), see [README.md](README.md#development).

**Type checking with gopls MCP (preferred when available):**
```bash
mcp__gopls__go_diagnostics
```

**Type checking fallback:**
```bash
go test -run=XXX_SHOULD_NEVER_MATCH_XXX ./...
```

**Format code:**
```bash
golines -w -m 150 .
```

## Code Architecture for Development

### Function Call Hierarchy

Understanding the sync flow when modifying code:

1. `runSync()` (main.go:90) - Cobra command handler, sets up context and logging
2. `syncResources()` (main.go:394) - Main orchestration
3. `syncTopics()` (main.go:414) - Iterates topics, filters, creates entities, returns accepted topic IDs
4. `syncSubscriptions()` (main.go:465) - Filters by accepted topics, creates entities, queues relationships
5. `manageRelationships()` (main.go:569) - Creates/deletes with deduplication
6. `updateEntityGroup()` (main.go:657) - Updates group for automatic cleanup

**Important patterns:**
- All `must*` functions exit with `os.Exit(1)` on fatal errors (don't return errors)
- Context cancellation checked in iterator loops via `checkCancellation()`
- GCP iterators use `iterator.Done` pattern for completion

### SYNQ Custom Entities API Patterns

**Custom Identifiers:**
```go
&entitiesv1.Identifier{
    Id: &entitiesv1.Identifier_Custom{
        Custom: &entitiesv1.CustomIdentifier{
            Id: fmt.Sprintf("pubsub::%s", topic.ID()),
        },
    },
}
```

**Relationship Deduplication:**
```go
// Use relationship.String() as map key for deduplication
existingMap := make(map[string]struct{})
for _, rel := range existing {
    existingMap[rel.String()] = struct{}{}
}

// Filter out relationships that already exist
var cleanedToCreate []*entitiescustomv1.Relationship
for _, rel := range toCreate {
    if _, exists := existingMap[rel.String()]; !exists {
        cleanedToCreate = append(cleanedToCreate, rel)
    }
}
```

### Resource Filtering Implementation

**Filter Interface:**
```go
type Filter interface {
    Accept(id string) bool
}
```

**IncludeExcludeFilter Logic:**
- If include list is empty, all items accepted by default
- Items must match at least one include filter (if any)
- Items matching any exclude filter are rejected (exclude takes precedence)

**Subscription Filtering (two stages):**
1. Parent topic must be in `acceptedTopicIds` map
2. Subscription ID must pass subscription filter

### Testing Patterns

Uses testify/suite:
```go
type FilterSuite struct {
    suite.Suite
}

func TestFilterSuite(t *testing.T) {
    suite.Run(t, new(FilterSuite))
}

func (s *FilterSuite) TestFilter() {
    s.Require().NoError(err)
    s.True(condition)
}
```

## Key Implementation Details

- Subscriptions use composite identifiers: `pubsub::<topic_id>::<subscription_id>`
- Entity groups enable automatic cleanup via API's automatic deletion of entities not in new group
- Relationships only managed for `pubsub::` prefixed entities (avoids touching other integrations)
- Icons validated as valid SVG XML before use
- Version info injected via ldflags: `version`, `commit`, `date`
- Context cancellation propagates to all operations for graceful shutdown
