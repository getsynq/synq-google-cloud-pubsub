package main

import (
	"testing"

	entitiescustomv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/custom/v1"
	entitiesv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/v1"
	"github.com/stretchr/testify/assert"
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

// TestNothingIsDeletedWhenTheRunComputedNothing is the reproducer for a sync
// that wiped another tool's lineage from a live workspace.
//
// Relationships are off by default, which leaves the desired set empty. An empty
// desired set used to mean every stored topic-to-subscription edge was unwanted,
// whoever had written it, so a routine `synq-google-cloud-pubsub` run with no
// flags deleted seven edges another integration had just published.
func TestNothingIsDeletedWhenTheRunComputedNothing(t *testing.T) {
	existing := []*entitiescustomv1.Relationship{
		edge("pubsub::prod.gcs.artefacts", "pubsub::prod.gcs.artefacts::prod.gcs.artefacts.consumer.subscription"),
	}

	_, toDelete := deduplicateRelationships(nil, existing)

	assert.Empty(t, edgeKeys(toDelete), "a run that computed no relationships must not delete any")
}

// TestAStaleSubscriptionEdgeIsDeleted keeps the reconciliation this tool is for:
// a subscription that is gone from Pub/Sub leaves an edge behind, and this run
// inventoried both of its ends, so it is this tool's to withdraw.
func TestAStaleSubscriptionEdgeIsDeleted(t *testing.T) {
	desired := []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::topic::topic.live.subscription"),
	}
	existing := []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::topic::topic.live.subscription"),
		edge("pubsub::topic", "pubsub::topic::topic.retired.subscription"),
	}

	toCreate, toDelete := deduplicateRelationships(desired, existing)

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
	existing := []*entitiescustomv1.Relationship{
		// A service catalog links its microservice entity to the topic.
		edge("pubsub::topic", "service::consumer"),
		// A bucket's notification edge, published by the Cloud Storage integration.
		edge("gcs::artefacts", "pubsub::topic"),
		// An edge between two Pub/Sub entities that is not a subscription of that topic.
		edge("pubsub::topic", "pubsub::other::other.sub.subscription"),
	}

	_, toDelete := deduplicateRelationships(desired, existing)

	assert.Empty(t, edgeKeys(toDelete))
}

// TestOnlySubscriptionsOfTheirOwnTopicAreOwned pins the shape test itself: a
// subscription's id is its topic's id plus the subscription name.
func TestOnlySubscriptionsOfTheirOwnTopicAreOwned(t *testing.T) {
	assert.True(t, ownsRelationship(edge("pubsub::topic", "pubsub::topic::topic.sub.subscription")))
	assert.False(t, ownsRelationship(edge("pubsub::topic", "pubsub::other::other.sub.subscription")))
	assert.False(t, ownsRelationship(edge("pubsub::topic", "service::consumer")))
	assert.False(t, ownsRelationship(edge("gcs::artefacts", "pubsub::topic")))
}

// TestAFilteredSubscriptionKeepsItsEdge is the other half of "only judge what
// this run saw": a subscription excluded by configuration is never inventoried,
// so its edge must not read as drift.
func TestAFilteredSubscriptionKeepsItsEdge(t *testing.T) {
	existing := []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::topic::topic.live.subscription"),
		edge("pubsub::topic", "pubsub::topic::topic.excluded.subscription"),
	}

	kept := withinInventory(existing, []*entitiesv1.Identifier{
		customID("pubsub::topic"),
		customID("pubsub::topic::topic.live.subscription"),
	})

	assert.Equal(t, []string{"pubsub::topic->pubsub::topic::topic.live.subscription"}, edgeKeys(kept))
}

// TestPruneWithdrawsOnlyWhatThisToolPublished covers the way back out. Turning
// relationships on was a deliberate act and so is undoing it, so prune is allowed
// to delete where a plain run with nothing to create is not — but it is still
// held to the same shape and inventory rules.
func TestPruneWithdrawsOnlyWhatThisToolPublished(t *testing.T) {
	existing := []*entitiescustomv1.Relationship{
		edge("pubsub::topic", "pubsub::topic::topic.live.subscription"),
		edge("pubsub::topic", "pubsub::topic::topic.other.subscription"),
		edge("pubsub::topic", "service::consumer"),
		edge("gcs::artefacts", "pubsub::topic"),
	}

	owned := ownedRelationships(existing)

	assert.Equal(t, []string{
		"pubsub::topic->pubsub::topic::topic.live.subscription",
		"pubsub::topic->pubsub::topic::topic.other.subscription",
	}, edgeKeys(owned))
}
