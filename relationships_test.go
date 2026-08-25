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
