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

1. `runSync()` - Cobra command handler, sets up context and logging
2. `syncResources()` - Main orchestration
3. `syncTopics()` - Iterates topics, filters, creates entities, returns accepted topic IDs
4. `syncSubscriptions()` - Filters by accepted topics, creates entities, queues relationships
5. `manageRelationships()` - Reconciles edges (see the reconciliation rules below)
6. `updateEntityGroup()` - Updates group for automatic cleanup

**Important patterns:**
- All `must*` functions exit with `os.Exit(1)` on fatal errors (don't return errors)
- Context cancellation checked in iterator loops via `checkCancellation()`
- Cancellation is not a destructive path, despite the partial inventory a
  cancelled scan returns: `manageRelationships` and `updateEntityGroup` issue
  their gRPC calls with the same context, and gRPC fails a call on a done
  context before the wire, so the run exits 1 instead of reconciling from what
  it managed to scan
- GCP iterators use `iterator.Done` pattern for completion

### Custom Entities API Patterns

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

**Relationship reconciliation — a run only withdraws what it computed.** All
four rules exist because breaking one is silent, and one of them was broken in a
released version: relationships are opt-in, the desired set is therefore empty by
default, and an empty desired set used to mean "delete every stored
topic-to-subscription edge", which wiped another integration's lineage from a
live workspace.

- `manageRelationships` is only called when `relationships.enabled` or
  `relationships.prune`.
- A run that computed no relationships withdraws none.
- `ownedEdge` is the shape test: a subscription's id is its topic's id plus the
  subscription name, so an edge to anything else touching that topic belongs to
  another producer. The same two cuts hand back the topic and subscription names
  the filters below are applied to.
- `withdrawableRelationships` keeps only the edges this run is answerable for:
  between a topic it scanned and a subscription its configuration would have
  published. Both ends are judged by the **filters** — the subscription filter
  and the relationship filter — not by whether an entity was inventoried, because
  the two reasons an edge has no entity behind it are opposites: excluded by
  configuration means leave the edge alone, deleted from Pub/Sub means withdraw
  it. Judging by the inventory made every deleted subscription leak its edge
  forever. Skipping the relationship filter here was worse than inconsistent: the
  desired set mixes topic edges with the BigQuery and bucket ones, and
  `staleRelationships` uses its emptiness as the sentinel, so an excluded topic
  edge was withdrawn whenever an unrelated delivery edge happened to exist.

`relationships_test.go` covers all four; the first test in it is the reproducer
for the released bug, and `TestAStaleSubscriptionEdgeIsDeleted` /
`TestAFilteredSubscriptionKeepsItsEdge` are the pair that pins the fourth apart.

**Why the feature is off by default**, and why `--relationships.prune` exists:
linking a topic to its subscriptions closes a cycle for every service that
consumes a topic it also publishes, so the catalog becomes hard to follow. Prune
is an explicit instruction to withdraw what this integration published, which is
why it may delete where a plain run with an empty desired set may not — it is
still held to `ownedEdge` and `withdrawableRelationships`.

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

`filter_test.go` uses testify/suite; everything written since uses plain
`func TestX(t *testing.T)` with `assert`/`require`. Prefer the plain form: one
named test per rule, its comment saying which rule and why it exists.

**Testing a flag** needs its own flag set. `LoadConfig` reads the global
`pflag.CommandLine`, so swap in a fresh `pflag.NewFlagSet`, call `InitFlags()`,
then `Parse` the args — `withFlags` in `config/config_test.go`.

**Testing the deployment or the GCP project** means clearing the environment
first, or the result depends on whose machine runs it: `clearDeploymentEnv`
(`QUALITY_REGION`, `QUALITY_HOME`, …) and `noGCPProject` (`GCP_PROJECT_ID`,
`CLOUDSDK_CONFIG`, and an empty `PATH` so gcloud is unreachable), both in
`auth_test.go`.

### Authentication

`auth.go` owns it. Credentials and the deployment come from
`github.com/getsynq/quality-oauth-go`, the same library every Coalesce Quality
CLI uses, so the credential store is shared and `--region` resolves identically
everywhere.

- Read environment variables through `qualityoauth.Getenv`, never `os.Getenv`, or
  that one setting stops honouring its `SYNQ_`-prefixed alias.
- Precedence is the library's, not this repo's: `Sources.Resolve` decides, and
  `auth_test.go` pins the tiers so a local change cannot quietly diverge. A tier
  never vetoes a higher one: an unusable `quality.region` is an error only when
  no flag supplied a deployment, and `LoadConfig` returns what it read alongside
  a validation error so `auth login --region` keeps working on a machine with no
  GCP project.
- `oauth_url` must be HTTPS, loopback aside. It overrides the token endpoint for
  whichever credentials the run resolved, the environment's included, so a config
  file naming a plain-HTTP host is a credential-exfiltration primitive.
- `App.FirstPartyClientID` stays empty. The authorization server seeds a client
  row per released first-party CLI and this is not one; an id it does not know is
  rejected at the authorize endpoint and the login then waits for a callback that
  never arrives.
- The `synq:` config section and the `--synq.*` flags are permanent aliases,
  merged in `config.QualityConfig.merge` as the lower-precedence source.

## Key Implementation Details

- Subscriptions use composite identifiers: `pubsub::<topic_id>::<subscription_id>`
- Entity groups enable automatic cleanup via API's automatic deletion of entities not in new group
- Relationships are only withdrawn for topic-to-subscription edges this run is configured to manage (see above)
- Icons validated as valid SVG XML before use
- Version info injected via ldflags: `version`, `commit`, `date`
- Context cancellation propagates to all operations for graceful shutdown

### Cross-Platform Lineage

The integration creates relationships to external platforms when subscriptions have delivery configured:

**BigQuery Lineage:**
- Triggered when `subscription.BigQueryConfig.Table` is set
- Creates relationship to BigQuery table using `BigqueryTableIdentifier`
- Table format: `project:dataset.table` or `project.dataset.table`
- Requires a native BigQuery integration in Coalesce Quality
- **Behavior**: Links to non-existent tables are **silently ignored**

**Cloud Storage Lineage:**
- Triggered when `subscription.CloudStorageConfig.Bucket` is set
- Creates relationship using custom identifier: `gcs::<bucket_name>`
- Requires GCS integration (https://github.com/getsynq/synq-google-cloud-storage)
- **Behavior**: Links to absent `gcs::*` entities are skipped with a debug log

**Excluding Cloud Storage relationships** is optional — a link to an absent
`gcs::*` entity is skipped with a debug log rather than failing the sync:
```yaml
relationships:
  filter:
    exclude:
      - '->gcs-.*'
      - '->bq-.*'
```
