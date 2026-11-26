package main

import (
	"context"
	"testing"

	entitiescustomv1grpc "buf.build/gen/go/getsynq/api/grpc/go/synq/entities/custom/v1/customv1grpc"
	entitiescustomv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/custom/v1"
	actorv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/issues/actor/v1"
	"github.com/getsynq/synq-google-cloud-pubsub/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// recordingTypes records every UpsertType request. Any other call panics on
// the nil embedded interface, so a test notices a write it did not expect.
type recordingTypes struct {
	entitiescustomv1grpc.TypesServiceClient
	requests []*entitiescustomv1.UpsertTypeRequest
}

func (r *recordingTypes) UpsertType(
	_ context.Context,
	req *entitiescustomv1.UpsertTypeRequest,
	_ ...grpc.CallOption,
) (*entitiescustomv1.UpsertTypeResponse, error) {
	r.requests = append(r.requests, req)
	return &entitiescustomv1.UpsertTypeResponse{}, nil
}

// recordingEntities records every UpsertEntity request.
type recordingEntities struct {
	entitiescustomv1grpc.EntitiesServiceClient
	requests []*entitiescustomv1.UpsertEntityRequest
}

func (r *recordingEntities) UpsertEntity(
	_ context.Context,
	req *entitiescustomv1.UpsertEntityRequest,
	_ ...grpc.CallOption,
) (*entitiescustomv1.UpsertEntityResponse, error) {
	r.requests = append(r.requests, req)
	return &entitiescustomv1.UpsertEntityResponse{}, nil
}

// TestEveryUpsertSendsTheWriter pins that each type and entity write names
// this tool and the GCP project as the writer. Without the actor, the server
// takes the writer from the token, and the app shows a client id instead of
// the sync that owns the entity.
func TestEveryUpsertSendsTheWriter(t *testing.T) {
	types := &recordingTypes{}
	entities := &recordingEntities{}
	clients := &qualityClients{
		types:    types,
		entities: entities,
		writer:   writerActor("my-project"),
	}

	cfg := &config.Config{}
	cfg.Types.TopicTypeID = 1
	cfg.Types.SubscriptionTypeID = 2

	ctx := context.Background()
	mustSetupEntityTypes(ctx, cfg, clients)
	clients.mustUpsertEntity(ctx, customID("pubsub::orders"), cfg.Types.TopicTypeID, "orders")
	clients.mustUpsertEntity(ctx, customID("pubsub::orders::billing"), cfg.Types.SubscriptionTypeID, "billing")

	want := &actorv1.Actor{
		Name: "synq-google-cloud-pubsub (my-project)",
		Via:  actorv1.Actor_VIA_API,
	}

	require.Len(t, types.requests, 2)
	for _, req := range types.requests {
		assert.True(t, proto.Equal(want, req.GetActor()), "type %q sent writer %v", req.GetType().GetName(), req.GetActor())
	}
	require.Len(t, entities.requests, 2)
	for _, req := range entities.requests {
		assert.True(t, proto.Equal(want, req.GetActor()), "entity %q sent writer %v", req.GetEntity().GetName(), req.GetActor())
	}
	assert.Empty(t, want.GetUser(), "no person runs the sync, so the writer has no user")
}

// TestADryRunWritesNothing pins that a nil client set is the dry run: the
// upsert logs and returns instead of dereferencing a client.
func TestADryRunWritesNothing(t *testing.T) {
	var clients *qualityClients
	assert.NotPanics(t, func() {
		clients.mustUpsertEntity(context.Background(), customID("pubsub::orders"), 1, "orders")
	})
}
