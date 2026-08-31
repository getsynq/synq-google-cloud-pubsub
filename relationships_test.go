package main

import (
	"testing"

	entitiescustomv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/custom/v1"
	entitiesv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/v1"
	"github.com/getsynq/synq-google-cloud-pubsub/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func customID(id string) *entitiesv1.Identifier {
	return &entitiesv1.Identifier{
		Id: &entitiesv1.Identifier_Custom{Custom: &entitiesv1.CustomIdentifier{Id: id}},
	}
}

func edge(upstream, downstream string) *entitiescustomv1.Relationship {
	return &entitiescustomv1.Relationship{Upstream: customID(upstream), Downstream: customID(downstream)}
}

func edgeKeys(rels []*entitiescustomv1.Relationship) []string {
	keys := make([]string, 0, len(rels))
	for _, rel := range rels {
		keys = append(keys, rel.Upstream.GetCustom().GetId()+"->"+rel.Downstream.GetCustom().GetId())
	}
	return keys
}

// acceptAll is the subscription filter of a run that configures no filters.
func acceptAll() Filter {
	return NewIncludeExcludeFilter(nil, nil)
}

// excluding builds the subscription filter of a run configured to skip a pattern.
func excluding(t *testing.T, pattern string) Filter {
	t.Helper()
	exclude, err := NewRegexFilter(pattern)
	require.NoError(t, err)
	return NewIncludeExcludeFilter(nil, []Filter{exclude})
}

// reconcile runs the two steps manageRelationships runs, in its order, so a test
// exercises the path a sync takes rather than one helper in isolation.
func reconcile(
	desired, stored []*entitiescustomv1.Relationship,
	acceptedTopicIds map[string]bool,
	subscriptionFilter, relationshipFilter Filter,
	mode relationshipMode,
) (toCreate, toDelete []*entitiescustomv1.Relationship) {
	withdrawable := withdrawableRelationships(stored, acceptedTopicIds, subscriptionFilter, relationshipFilter)
	return reconcileRelationships(desired, stored, withdrawable, mode)
}

// bigQueryEdge is a delivery relationship, the shape this tool publishes to an
// entity it does not own.
func bigQueryEdge(subscription string) *entitiescustomv1.Relationship {
	return &entitiescustomv1.Relationship{
		Upstream: customID(subscription),
		Downstream: &entitiesv1.Identifier{
			Id: &entitiesv1.Identifier_BigqueryTable{
				BigqueryTable: &entitiesv1.BigqueryTableIdentifier{Project: "p", Dataset: "d", Table: "t"},
			},
		},
	}
}

// TestNothingIsDeletedWhenRelationshipsAreOff is the reproducer for a sync that
// wiped another tool's lineage from a live workspace.
//
// Relationships are off by default, which leaves the desired set empty. An empty
// desired set used to mean every stored topic-to-subscription edge was unwanted,
// whoever had written it, so a routine `synq-google-cloud-pubsub` run with no
// flags deleted seven edges another integration had just published.
//
// The rule is the mode, not the emptiness. TestTheLastSubscriptionOfATopicLosesItsEdge
// is the run that also computes nothing and must withdraw.
func TestNothingIsDeletedWhenRelationshipsAreOff(t *testing.T) {
	stored := []*entitiescustomv1.Relationship{
		edge("pubsub::prod.gcs.artefacts", "pubsub::prod.gcs.artefacts::prod.gcs.artefacts.consumer.subscription"),
	}

	toCreate, toDelete := reconcile(
		nil, stored,
		map[string]bool{"prod.gcs.artefacts": true},
		acceptAll(), acceptAll(),
		relationshipsOff,
	)

	assert.Empty(t, edgeKeys(toDelete), "a run with relationships off must not delete any")
	assert.Empty(t, toCreate)
}

// TestRelationshipsAreOffUnlessAskedFor pins the default the rule above now
// rests on, since the mode is what protects it.
func TestRelationshipsAreOffUnlessAskedFor(t *testing.T) {
	assert.Equal(t, relationshipsOff, relationshipModeFor(&config.Config{}))
	assert.Equal(t, relationshipsReconcile, relationshipModeFor(&config.Config{
		Relationships: config.RelationshipsConfig{Enabled: true},
	}))
	assert.Equal(t, relationshipsPrune, relationshipModeFor(&config.Config{
		Relationships: config.RelationshipsConfig{Prune: true},
	}))
	// Withdrawing and publishing at once would republish what it just withdrew.
	assert.Equal(t, relationshipsPrune, relationshipModeFor(&config.Config{
		Relationships: config.RelationshipsConfig{Enabled: true, Prune: true},
	}))
}

// TestTheLastSubscriptionOfATopicLosesItsEdge is the other run that computes
// nothing, and the reason an empty desired set cannot be the rule: a topic whose
// only subscription was deleted leaves an edge this run scanned the topic for,
// and withdrawing it is what the feature is for. It kept that edge forever, with
// --relationships.prune the only way out.
func TestTheLastSubscriptionOfATopicLosesItsEdge(t *testing.T) {
	stored := []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::topic::topic.retired.subscription"),
	}

	_, toDelete := reconcile(
		nil, stored,
		map[string]bool{"topic": true},
		acceptAll(), acceptAll(),
		relationshipsReconcile,
	)

	assert.Equal(t, []string{"pubsub::topic->pubsub::topic::topic.retired.subscription"}, edgeKeys(toDelete))
}

// TestAStaleSubscriptionEdgeIsDeleted keeps the reconciliation this tool is for:
// a subscription that is gone from Pub/Sub leaves an edge behind, and withdrawing
// it is the job.
//
// The deleted subscription has no entity for the run to inventory, which is why
// the withdrawable set is decided by the subscription filter and not by the
// inventory: judging it by the inventory left the stale edge in the workspace on
// every subsequent run.
func TestAStaleSubscriptionEdgeIsDeleted(t *testing.T) {
	desired := []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::topic::topic.live.subscription"),
	}
	stored := []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::topic::topic.live.subscription"),
		edge("pubsub::topic", "pubsub::topic::topic.retired.subscription"),
	}

	toCreate, toDelete := reconcile(desired, stored, map[string]bool{"topic": true}, acceptAll(), acceptAll(), relationshipsReconcile)

	assert.Empty(t, toCreate, "an edge that already exists is not created again")
	assert.Equal(t, []string{"pubsub::topic->pubsub::topic::topic.retired.subscription"}, edgeKeys(toDelete))
}

// TestAnotherProducersEdgeSurvives covers the entities this tool points at but
// does not own. Listing the stored edges by topic also returns everything else
// touching that topic, and deleting those makes this sync undo another
// integration's lineage on every run.
func TestAnotherProducersEdgeSurvives(t *testing.T) {
	desired := []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::topic::topic.live.subscription"),
	}
	stored := []*entitiescustomv1.Relationship{
		// A service catalog links its microservice entity to the topic.
		edge("pubsub::topic", "service::consumer"),
		// A bucket's notification edge, published by the Cloud Storage integration.
		edge("gcs::artefacts", "pubsub::topic"),
		// An edge between two Pub/Sub entities that is not a subscription of that topic.
		edge("pubsub::topic", "pubsub::other::other.sub.subscription"),
	}

	_, toDelete := reconcile(desired, stored, map[string]bool{"topic": true, "other": true}, acceptAll(), acceptAll(), relationshipsReconcile)

	assert.Empty(t, edgeKeys(toDelete))
}

// TestOnlySubscriptionsOfTheirOwnTopicAreOwned pins the shape test itself: a
// subscription's id is its topic's id plus the subscription name. It pins the two
// names that fall out of it as well, because the filters are applied to those.
func TestOnlySubscriptionsOfTheirOwnTopicAreOwned(t *testing.T) {
	topic, subscription, ok := ownedEdge(edge("pubsub::topic", "pubsub::topic::topic.sub.subscription"))
	assert.True(t, ok)
	assert.Equal(t, "topic", topic)
	assert.Equal(t, "topic.sub.subscription", subscription)

	for _, rel := range []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::other::other.sub.subscription"),
		edge("pubsub::topic", "service::consumer"),
		edge("gcs::artefacts", "pubsub::topic"),
	} {
		_, _, ok := ownedEdge(rel)
		assert.False(t, ok, edgeKeys([]*entitiescustomv1.Relationship{rel})[0])
	}
}

// TestAFilteredSubscriptionKeepsItsEdge is the other half of "only judge what
// this run is configured to manage": a subscription excluded by configuration is
// not this run's to touch, so its edge must not read as drift. It is the case a
// deleted subscription looks identical to from the inventory alone.
func TestAFilteredSubscriptionKeepsItsEdge(t *testing.T) {
	desired := []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::topic::topic.live.subscription"),
	}
	stored := []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::topic::topic.live.subscription"),
		edge("pubsub::topic", "pubsub::topic::topic.excluded.subscription"),
	}

	_, toDelete := reconcile(desired, stored, map[string]bool{"topic": true}, excluding(t, `\.excluded\.`), acceptAll(), relationshipsReconcile)

	assert.Empty(t, edgeKeys(toDelete))
}

// TestATopicThisRunNeverScannedKeepsItsEdges is the same rule one level up: a
// topic excluded by configuration, or one the scan never reached, is not this
// run's to judge either.
func TestATopicThisRunNeverScannedKeepsItsEdges(t *testing.T) {
	stored := []*entitiescustomv1.Relationship{
		edge("pubsub::unscanned", "pubsub::unscanned::unscanned.sub.subscription"),
	}

	withdrawable := withdrawableRelationships(stored, map[string]bool{"topic": true}, acceptAll(), acceptAll())

	assert.Empty(t, edgeKeys(withdrawable))
}

// TestPruneWithdrawsOnlyWhatThisToolPublished covers the way back out. Turning
// relationships on was a deliberate act and so is undoing it, so prune is allowed
// to delete where a plain run with nothing to create is not — but it is still
// held to the same ownership and configuration rules.
func TestPruneWithdrawsOnlyWhatThisToolPublished(t *testing.T) {
	stored := []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::topic::topic.live.subscription"),
		edge("pubsub::topic", "pubsub::topic::topic.retired.subscription"),
		edge("pubsub::topic", "pubsub::topic::topic.excluded.subscription"),
		edge("pubsub::topic", "service::consumer"),
		edge("gcs::artefacts", "pubsub::topic"),
	}

	toCreate, toDelete := reconcile(
		nil, stored,
		map[string]bool{"topic": true},
		excluding(t, `\.excluded\.`), acceptAll(),
		relationshipsPrune,
	)

	assert.Empty(t, toCreate, "prune withdraws and creates nothing")
	assert.Equal(t, []string{
		"pubsub::topic->pubsub::topic::topic.live.subscription",
		"pubsub::topic->pubsub::topic::topic.retired.subscription",
	}, edgeKeys(toDelete))
}

// TestAnExistingCrossPlatformEdgeIsNotUpsertedAgain guards the create side. A
// BigQuery or bucket edge is not one this tool may withdraw, so it is absent
// from the withdrawable set; checking the creates against that set instead of
// against everything stored would republish it on every run.
func TestAnExistingCrossPlatformEdgeIsNotUpsertedAgain(t *testing.T) {
	bq := bigQueryEdge("pubsub::topic::topic.live.subscription")
	desired := []*entitiescustomv1.Relationship{bq}
	stored := []*entitiescustomv1.Relationship{bq}

	toCreate, toDelete := reconcile(desired, stored, map[string]bool{"topic": true}, acceptAll(), acceptAll(), relationshipsReconcile)

	assert.Empty(t, toCreate)
	assert.Empty(t, toDelete)
}

// TestAnExcludedRelationshipKeepsItsEdge is the reproducer for a filter that
// deleted the very edges it was written to leave out.
//
// The desired set mixes topic edges with the BigQuery and Cloud Storage delivery
// edges, and staleRelationships used its emptiness as the safety sentinel. A
// relationship filter that excluded the topic edges therefore withdrew every
// stored one as soon as a single delivery edge existed to make the set non-empty,
// and withdrew nothing when none did. Same configuration, opposite outcome,
// decided by unrelated data.
func TestAnExcludedRelationshipKeepsItsEdge(t *testing.T) {
	desired := []*entitiescustomv1.Relationship{
		bigQueryEdge("pubsub::topic::topic.live.subscription"),
	}
	stored := []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::topic::topic.live.subscription"),
		bigQueryEdge("pubsub::topic::topic.live.subscription"),
	}

	_, toDelete := reconcile(
		desired, stored,
		map[string]bool{"topic": true},
		acceptAll(),
		excluding(t, `->.*\.subscription$`),
		relationshipsReconcile,
	)

	assert.Empty(t, edgeKeys(toDelete), "an edge the relationship filter excludes is not this run's to withdraw")
}
