package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/xml"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	entitiescustomv1grpc "buf.build/gen/go/getsynq/api/grpc/go/synq/entities/custom/v1/customv1grpc"
	entitiescustomv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/custom/v1"
	entitiesv1 "buf.build/gen/go/getsynq/api/protocolbuffers/go/synq/entities/v1"
	"cloud.google.com/go/pubsub" //nolint:staticcheck // TODO: Migrate to pubsub/v2 - requires API changes
	"github.com/getsynq/synq-google-cloud-pubsub/config"
	"github.com/joho/godotenv"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

//go:embed icons/pubsub.svg
var defaultPubSubIcon []byte

// Version information (set via ldflags during build)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// ============================================================================
// Main Entry Point
// ============================================================================

var rootCmd = &cobra.Command{
	Use:   toolName,
	Short: "Sync Google Cloud Pub/Sub resources to Coalesce Quality",
	Long: `A Google Cloud Pub/Sub integration that publishes topics and subscriptions
as custom entities in Coalesce Quality.

The tool supports configuration through:
  - YAML config file (config.yaml by default)
  - Environment variables (QUALITY_CLIENT_ID, etc.)
  - Command-line flags (highest precedence)

Configuration precedence: defaults → config file → environment variables → flags

Authenticate with a browser login (` + toolName + ` auth login), client
credentials in QUALITY_CLIENT_ID and QUALITY_CLIENT_SECRET, or a pre-issued
QUALITY_TOKEN. A browser login is shared with the other Coalesce Quality tools.`,
	Version: version,
	RunE:    runSync,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("%s %s\n", toolName, version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
	},
}

func main() {
	// Load .env file if it exists
	_ = godotenv.Load()

	// Initialize configuration flags
	config.InitFlags()

	// Add subcommands
	rootCmd.AddCommand(versionCmd, authCmd)

	// Execute the root command
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runSync orchestrates the sync process
func runSync(cmd *cobra.Command, args []string) error {
	// Get context (Cobra handles signal cancellation if set up)
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Setup context with cancellation and signal handling
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Setup logging and handle graceful shutdown
	setupLogging(ctx)
	handleShutdown(ctx, cancel)

	logger := slog.Default()
	logger.InfoContext(ctx, "Starting Google Cloud Pub/Sub integration")

	// Get config path from flags
	configPath, _ := cmd.Flags().GetString("config")

	// Load configuration
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to load configuration", slog.String("error", err.Error()))
		return err
	}

	if len(cfg.DeprecatedKeys) > 0 {
		logger.WarnContext(ctx, "Configuration uses superseded keys; the quality section replaces them",
			slog.Any("keys", cfg.DeprecatedKeys),
		)
	}

	// Show dry-run mode warning
	if cfg.DryRun {
		logger.InfoContext(ctx, "DRY-RUN MODE: Will scan GCP resources but not call the Coalesce Quality API")
	}

	// Setup clients
	pubsubClient := mustCreatePubSubClient(ctx, cfg)
	defer func() {
		if err := pubsubClient.Close(); err != nil {
			logger.ErrorContext(ctx, "Error closing Pub/Sub client", slog.String("error", err.Error()))
		}
	}()

	var qualityClients *qualityClients
	if !cfg.DryRun {
		qualityClients = mustCreateQualityClients(ctx, cfg)
		defer func() {
			if err := qualityClients.close(); err != nil {
				logger.ErrorContext(ctx, "Error closing Coalesce Quality clients", slog.String("error", err.Error()))
			}
		}()

		// Setup entity types in Coalesce Quality
		mustSetupEntityTypes(ctx, cfg, qualityClients.types)
	}

	// Build filters from configuration
	filters := buildFilters(cfg)

	// Sync Pub/Sub resources to Coalesce Quality
	stats := syncResources(ctx, cfg, pubsubClient, qualityClients, filters)

	logger.InfoContext(ctx, "Sync completed successfully",
		slog.Int("topics", stats.Topics),
		slog.Int("subscriptions", stats.Subscriptions),
		slog.Int("total_entities", stats.TotalEntities),
	)

	return nil
}

// ============================================================================
// Types
// ============================================================================

// qualityClients holds every Coalesce Quality API client a sync uses
type qualityClients struct {
	conn          *grpc.ClientConn
	types         entitiescustomv1grpc.TypesServiceClient
	entities      entitiescustomv1grpc.EntitiesServiceClient
	relationships entitiescustomv1grpc.RelationshipsServiceClient
	groups        entitiescustomv1grpc.GroupsServiceClient
}

func (c *qualityClients) close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// filters holds all configured filters
type filters struct {
	topics        Filter
	subscriptions Filter
	relationships Filter
}

// syncStats tracks sync statistics
type syncStats struct {
	Topics        int
	Subscriptions int
	TotalEntities int
}

// ============================================================================
// Configuration and Setup
// ============================================================================

// handleShutdown sets up graceful shutdown on interrupt signal
func handleShutdown(ctx context.Context, cancel context.CancelFunc) {
	logger := slog.Default()
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt)
		<-sigChan
		logger.InfoContext(ctx, "Received interrupt signal, shutting down...")
		cancel()
	}()
}

// buildFilters creates all filters from configuration
func buildFilters(cfg *config.Config) *filters {
	return &filters{
		topics:        buildIncludeExcludeFilter(cfg.Filter.Topics),
		subscriptions: buildIncludeExcludeFilter(cfg.Filter.Subscriptions),
		relationships: buildIncludeExcludeFilter(cfg.Relationships.Filter),
	}
}

// buildIncludeExcludeFilter creates a filter from include/exclude patterns
func buildIncludeExcludeFilter(rules config.FilterRules) Filter {
	var includeFilters []Filter
	for _, pattern := range rules.Include {
		includeFilters = append(includeFilters, lo.Must(NewRegexFilter(pattern)))
	}

	var excludeFilters []Filter
	for _, pattern := range rules.Exclude {
		excludeFilters = append(excludeFilters, lo.Must(NewRegexFilter(pattern)))
	}

	return NewIncludeExcludeFilter(includeFilters, excludeFilters)
}

// ============================================================================
// Client Setup
// ============================================================================

// mustCreatePubSubClient creates a Pub/Sub client or exits on error
func mustCreatePubSubClient(ctx context.Context, cfg *config.Config) *pubsub.Client {
	logger := slog.Default()
	logger.InfoContext(ctx, "Connecting to Google Cloud Pub/Sub", slog.String("project", cfg.GCP.ProjectID))

	client, err := pubsub.NewClient(ctx, cfg.GCP.ProjectID, option.WithUserAgent(cfg.GCP.UserAgent))
	if err != nil {
		logger.ErrorContext(ctx, "Failed to create Pub/Sub client", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.InfoContext(ctx, "Successfully connected to Google Cloud Pub/Sub")
	return client
}

// mustCreateQualityClients resolves credentials and opens the API clients, or exits
func mustCreateQualityClients(ctx context.Context, cfg *config.Config) *qualityClients {
	logger := slog.Default()

	target, err := resolveTarget(cfg)
	if err != nil {
		logger.ErrorContext(ctx, "Could not resolve the Coalesce Quality deployment", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.InfoContext(ctx, "Authenticating with the Coalesce Quality API", slog.String("deployment", target.String()))

	conn, err := connect(ctx, cfg, target)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to connect to the Coalesce Quality API", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.InfoContext(ctx, "Successfully connected to the Coalesce Quality API")

	return &qualityClients{
		conn:          conn,
		types:         entitiescustomv1grpc.NewTypesServiceClient(conn),
		entities:      entitiescustomv1grpc.NewEntitiesServiceClient(conn),
		relationships: entitiescustomv1grpc.NewRelationshipsServiceClient(conn),
		groups:        entitiescustomv1grpc.NewGroupsServiceClient(conn),
	}
}

// ============================================================================
// Entity Type Management
// ============================================================================

// mustSetupEntityTypes creates or updates the custom entity types
func mustSetupEntityTypes(ctx context.Context, cfg *config.Config, typesClient entitiescustomv1grpc.TypesServiceClient) {
	logger := slog.Default()

	// Load icons
	logger.InfoContext(ctx, "Loading entity type icons")
	topicIcon := mustLoadIcon(ctx, cfg.Types.TopicIcon, defaultPubSubIcon)
	subscriptionIcon := mustLoadIcon(ctx, cfg.Types.SubscriptionIcon, defaultPubSubIcon)

	logger.DebugContext(ctx, "Icons loaded successfully",
		slog.Bool("topic_custom", cfg.Types.TopicIcon != ""),
		slog.Bool("subscription_custom", cfg.Types.SubscriptionIcon != ""),
	)

	// Create/update entity types
	logger.InfoContext(ctx, "Creating/updating entity types in Coalesce Quality",
		slog.Int("topic_type_id", int(cfg.Types.TopicTypeID)),
		slog.Int("subscription_type_id", int(cfg.Types.SubscriptionTypeID)),
	)

	mustUpsertType(ctx, typesClient, cfg.Types.TopicTypeID, "Topic", topicIcon)
	mustUpsertType(ctx, typesClient, cfg.Types.SubscriptionTypeID, "Subscription", subscriptionIcon)

	logger.InfoContext(ctx, "Entity types created successfully")
}

// mustUpsertType creates or updates a single entity type
func mustUpsertType(ctx context.Context, client entitiescustomv1grpc.TypesServiceClient, typeID int32, name string, icon []byte) {
	logger := slog.Default()

	_, err := client.UpsertType(ctx, &entitiescustomv1.UpsertTypeRequest{
		Type: &entitiesv1.Type{
			TypeId:  typeID,
			Name:    name,
			SvgIcon: icon,
		},
	})
	if err != nil {
		logger.ErrorContext(ctx, "Failed to create entity type", slog.String("name", name), slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// mustLoadIcon loads an icon or exits on error
func mustLoadIcon(ctx context.Context, customPath string, defaultIcon []byte) []byte {
	icon, err := loadIcon(customPath, defaultIcon)
	if err != nil {
		logger := slog.Default()
		logger.ErrorContext(ctx, "Failed to load icon", slog.String("error", err.Error()))
		os.Exit(1)
	}
	return icon
}

// loadIcon loads an icon from a custom path if provided, otherwise returns the default embedded icon
func loadIcon(customPath string, defaultIcon []byte) ([]byte, error) {
	var iconData []byte
	var err error

	if customPath == "" {
		iconData = defaultIcon
	} else {
		iconData, err = os.ReadFile(customPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read custom icon from %s: %w", customPath, err)
		}
	}

	// Validate that the icon is valid SVG
	if err := validateSVG(iconData); err != nil {
		return nil, fmt.Errorf("invalid SVG icon: %w", err)
	}

	return iconData, nil
}

// validateSVG checks if the provided data is valid SVG XML
func validateSVG(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("icon data is empty")
	}

	trimmed := bytes.TrimSpace(data)
	lowerData := bytes.ToLower(trimmed)

	if !bytes.HasPrefix(lowerData, []byte("<svg")) && !bytes.HasPrefix(lowerData, []byte("<?xml")) {
		return fmt.Errorf("icon does not start with <svg> or <?xml> tag")
	}

	if !bytes.Contains(lowerData, []byte("<svg")) {
		return fmt.Errorf("icon does not contain <svg> tag")
	}

	// Validate as XML
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = false

	for {
		_, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("invalid XML structure: %w", err)
		}
	}

	return nil
}

// ============================================================================
// Resource Sync
// ============================================================================

// syncResources publishes every Pub/Sub resource
func syncResources(ctx context.Context, cfg *config.Config, pubsubClient *pubsub.Client, clients *qualityClients, filters *filters) syncStats {
	var entitiesClient entitiescustomv1grpc.EntitiesServiceClient
	if clients != nil {
		entitiesClient = clients.entities
	}

	// Sync topics and subscriptions
	createdEntities, acceptedTopics, topicCount := syncTopics(ctx, cfg, pubsubClient, entitiesClient, filters.topics)

	// List existing custom entities for relationship validation (only if relationships are enabled)
	var customEntities map[string]bool
	if cfg.Relationships.Enabled && entitiesClient != nil {
		var err error
		customEntities, err = listCustomEntities(ctx, entitiesClient)
		if err != nil {
			slog.Default().WarnContext(ctx, "Failed to list custom entities", slog.String("error", err.Error()))
			customEntities = make(map[string]bool) // Use empty map on error
		}
	}

	subscriptionCount, relationshipsToCreate := syncSubscriptions(ctx, cfg, pubsubClient, entitiesClient, filters, acceptedTopics, &createdEntities, customEntities)

	// Manage relationships and entity groups only if not in dry-run mode
	if clients != nil {
		if cfg.Relationships.Enabled || cfg.Relationships.Prune {
			manageRelationships(
				ctx,
				clients.relationships,
				createdEntities,
				relationshipsToCreate,
				acceptedTopics,
				filters.subscriptions,
				cfg.Relationships.Prune,
			)
		}
		updateEntityGroup(ctx, cfg, clients.groups, createdEntities)
	}

	return syncStats{
		Topics:        topicCount,
		Subscriptions: subscriptionCount,
		TotalEntities: len(createdEntities),
	}
}

// syncTopics publishes every topic
func syncTopics(
	ctx context.Context,
	cfg *config.Config,
	pubsubClient *pubsub.Client,
	entitiesClient entitiescustomv1grpc.EntitiesServiceClient,
	topicFilter Filter,
) ([]*entitiesv1.Identifier, map[string]bool, int) {
	logger := slog.Default()
	logger.InfoContext(ctx, "Scanning Pub/Sub topics")

	var createdEntities []*entitiesv1.Identifier
	acceptedTopicIds := make(map[string]bool)
	topicCount := 0

	topicsIterator := pubsubClient.Topics(ctx)
	for {
		// Check for cancellation
		if err := checkCancellation(ctx); err != nil {
			return createdEntities, acceptedTopicIds, topicCount
		}

		topic, err := topicsIterator.NextConfig()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			logger.ErrorContext(ctx, "Error iterating topics", slog.String("error", err.Error()))
			os.Exit(1)
		}

		if !topicFilter.Accept(topic.ID()) {
			logger.DebugContext(ctx, "Skipping filtered topic", slog.String("topic", topic.ID()))
			continue
		}

		topicCount++
		logger.DebugContext(ctx, "Processing topic", slog.String("topic", topic.ID()))

		topicID := &entitiesv1.Identifier{
			Id: &entitiesv1.Identifier_Custom{
				Custom: &entitiesv1.CustomIdentifier{
					Id: fmt.Sprintf("pubsub::%s", topic.ID()),
				},
			},
		}

		createdEntities = append(createdEntities, topicID)
		acceptedTopicIds[topic.ID()] = true

		mustUpsertEntity(ctx, entitiesClient, topicID, cfg.Types.TopicTypeID, topic.ID())
	}

	logger.InfoContext(ctx, "Topics processed", slog.Int("count", topicCount))
	return createdEntities, acceptedTopicIds, topicCount
}

// syncSubscriptions publishes every subscription
func syncSubscriptions(
	ctx context.Context,
	cfg *config.Config,
	pubsubClient *pubsub.Client,
	entitiesClient entitiescustomv1grpc.EntitiesServiceClient,
	filters *filters,
	acceptedTopicIds map[string]bool,
	createdEntities *[]*entitiesv1.Identifier,
	customEntities map[string]bool,
) (int, []*entitiescustomv1.Relationship) {
	logger := slog.Default()
	logger.InfoContext(ctx, "Scanning Pub/Sub subscriptions")

	subscriptionCount := 0
	var relationshipsToCreate []*entitiescustomv1.Relationship

	subscriptionsIterator := pubsubClient.Subscriptions(ctx)
	for {
		// Check for cancellation
		if err := checkCancellation(ctx); err != nil {
			return subscriptionCount, relationshipsToCreate
		}

		subscription, err := subscriptionsIterator.NextConfig()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			logger.ErrorContext(ctx, "Error iterating subscriptions", slog.String("error", err.Error()))
			os.Exit(1)
		}

		if !acceptedTopicIds[subscription.Topic.ID()] {
			logger.DebugContext(ctx, "Skipping subscription for non-accepted topic",
				slog.String("subscription", subscription.ID()),
				slog.String("topic", subscription.Topic.ID()),
			)
			continue
		}

		if !filters.subscriptions.Accept(subscription.ID()) {
			logger.DebugContext(ctx, "Skipping filtered subscription", slog.String("subscription", subscription.ID()))
			continue
		}

		subscriptionCount++
		logger.DebugContext(ctx, "Processing subscription",
			slog.String("subscription", subscription.ID()),
			slog.String("topic", subscription.Topic.ID()),
		)

		topicID := &entitiesv1.Identifier{
			Id: &entitiesv1.Identifier_Custom{
				Custom: &entitiesv1.CustomIdentifier{
					Id: fmt.Sprintf("pubsub::%s", subscription.Topic.ID()),
				},
			},
		}

		subscriptionID := &entitiesv1.Identifier{
			Id: &entitiesv1.Identifier_Custom{
				Custom: &entitiesv1.CustomIdentifier{
					Id: fmt.Sprintf("pubsub::%s::%s", subscription.Topic.ID(), subscription.ID()),
				},
			},
		}

		*createdEntities = append(*createdEntities, subscriptionID)
		mustUpsertEntity(ctx, entitiesClient, subscriptionID, cfg.Types.SubscriptionTypeID, truncate(subscription.ID(), 100))

		// Create relationship if enabled and passes filter
		if cfg.Relationships.Enabled {
			// Topic -> Subscription relationship
			relationshipKey := fmt.Sprintf("%s->%s", subscription.Topic.ID(), subscription.ID())
			if filters.relationships.Accept(relationshipKey) {
				relationshipsToCreate = append(relationshipsToCreate, &entitiescustomv1.Relationship{
					Upstream:   topicID,
					Downstream: subscriptionID,
				})
				logger.DebugContext(ctx, "Queued relationship",
					slog.String("topic", subscription.Topic.ID()),
					slog.String("subscription", subscription.ID()),
				)
			}

			// BigQuery delivery: Subscription -> BigQuery Table relationship
			// BigQuery entities are not custom entities and are safe to link
			if subscription.BigQueryConfig.Table != "" {
				// Parse BigQuery table reference (format: projectId.datasetId.tableId or projectId:datasetId.tableId)
				tableParts := strings.ReplaceAll(subscription.BigQueryConfig.Table, ":", ".")
				parts := strings.Split(tableParts, ".")
				if len(parts) == 3 {
					bqTableID := &entitiesv1.Identifier{
						Id: &entitiesv1.Identifier_BigqueryTable{
							BigqueryTable: &entitiesv1.BigqueryTableIdentifier{
								Project: parts[0],
								Dataset: parts[1],
								Table:   parts[2],
							},
						},
					}

					relationshipKey := fmt.Sprintf("%s->bq-%s", subscription.ID(), subscription.BigQueryConfig.Table)
					if filters.relationships.Accept(relationshipKey) {
						relationshipsToCreate = append(relationshipsToCreate, &entitiescustomv1.Relationship{
							Upstream:   subscriptionID,
							Downstream: bqTableID,
						})
						logger.DebugContext(ctx, "Queued BigQuery relationship",
							slog.String("subscription", subscription.ID()),
							slog.String("bigquery_table", subscription.BigQueryConfig.Table),
						)
					}
				}
			}

			// Cloud Storage delivery: Subscription -> GCS Bucket relationship
			// Check if GCS bucket entity exists (GCS entities are custom entities)
			if subscription.CloudStorageConfig.Bucket != "" {
				gcsCustomID := fmt.Sprintf("gcs::%s", subscription.CloudStorageConfig.Bucket)

				// Check if GCS bucket entity exists in the custom entities map
				if customEntities == nil || customEntities[gcsCustomID] {
					gcsID := &entitiesv1.Identifier{
						Id: &entitiesv1.Identifier_Custom{
							Custom: &entitiesv1.CustomIdentifier{
								Id: gcsCustomID,
							},
						},
					}

					relationshipKey := fmt.Sprintf("%s->gcs-%s", subscription.ID(), subscription.CloudStorageConfig.Bucket)
					if filters.relationships.Accept(relationshipKey) {
						relationshipsToCreate = append(relationshipsToCreate, &entitiescustomv1.Relationship{
							Upstream:   subscriptionID,
							Downstream: gcsID,
						})
						logger.DebugContext(ctx, "Queued GCS relationship",
							slog.String("subscription", subscription.ID()),
							slog.String("gcs_bucket", subscription.CloudStorageConfig.Bucket),
						)
					}
				} else {
					logger.DebugContext(ctx, "Skipping GCS relationship - bucket entity not found",
						slog.String("subscription", subscription.ID()),
						slog.String("gcs_bucket", subscription.CloudStorageConfig.Bucket),
					)
				}
			}
		}
	}

	logger.InfoContext(ctx, "Subscriptions processed", slog.Int("count", subscriptionCount))
	return subscriptionCount, relationshipsToCreate
}

// mustUpsertEntity creates or updates an entity
func mustUpsertEntity(ctx context.Context, client entitiescustomv1grpc.EntitiesServiceClient, id *entitiesv1.Identifier, typeID int32, name string) {
	logger := slog.Default()

	// Skip the API call in dry-run mode
	if client == nil {
		logger.DebugContext(ctx, "[DRY-RUN] Would upsert entity",
			slog.String("name", name),
			slog.Int("type_id", int(typeID)),
			slog.String("id", id.GetCustom().GetId()),
		)
		return
	}

	_, err := client.UpsertEntity(ctx, &entitiescustomv1.UpsertEntityRequest{
		Entity: &entitiesv1.Entity{
			Id:        id,
			TypeId:    typeID,
			Name:      name,
			CreatedAt: timestamppb.Now(),
		},
	})
	if err != nil {
		logger.ErrorContext(ctx, "Failed to create entity", slog.String("name", name), slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// ============================================================================
// Relationship Management
// ============================================================================

// manageRelationships reconciles the topic-to-subscription edges.
//
// prune inverts it: the run withdraws every edge it owns and creates none. That
// is an explicit instruction rather than an empty desired set, which is why it
// is allowed to delete where a plain run with nothing to create is not — the
// operator asked for the graph to lose these edges, having decided the topic and
// its subscriptions read better apart.
func manageRelationships(
	ctx context.Context,
	client entitiescustomv1grpc.RelationshipsServiceClient,
	createdEntities []*entitiesv1.Identifier,
	relationshipsToCreate []*entitiescustomv1.Relationship,
	acceptedTopicIds map[string]bool,
	subscriptionFilter Filter,
	prune bool,
) {
	logger := slog.Default()
	logger.InfoContext(ctx, "Retrieving existing relationships")

	listResp, err := client.ListRelationships(ctx, &entitiescustomv1.ListRelationshipsRequest{
		Ids: createdEntities,
	})
	if err != nil {
		logger.ErrorContext(ctx, "Failed to list relationships", slog.String("error", err.Error()))
		os.Exit(1)
	}

	withdrawable := withdrawableRelationships(listResp.Relationships, acceptedTopicIds, subscriptionFilter)

	var toCreate, toDelete []*entitiescustomv1.Relationship
	if prune {
		toDelete = withdrawable
	} else {
		toCreate = missingRelationships(relationshipsToCreate, listResp.Relationships)
		toDelete = staleRelationships(relationshipsToCreate, withdrawable)
	}

	logger.InfoContext(ctx, "Managing relationships",
		slog.Int("to_create", len(toCreate)),
		slog.Int("to_delete", len(toDelete)),
	)

	if len(toCreate) > 0 {
		_, err = client.UpsertRelationships(ctx, &entitiescustomv1.UpsertRelationshipsRequest{
			Relationships: toCreate,
		})
		if err != nil {
			logger.ErrorContext(ctx, "Failed to create relationships", slog.String("error", err.Error()))
			os.Exit(1)
		}
		logger.InfoContext(ctx, "Relationships created", slog.Int("count", len(toCreate)))
	}

	if len(toDelete) > 0 {
		_, err = client.DeleteRelationships(ctx, &entitiescustomv1.DeleteRelationshipsRequest{
			Relationships: toDelete,
		})
		if err != nil {
			logger.ErrorContext(ctx, "Failed to delete relationships", slog.String("error", err.Error()))
			os.Exit(1)
		}
		logger.InfoContext(ctx, "Relationships deleted", slog.Int("count", len(toDelete)))
	}
}

// missingRelationships returns the desired edges the workspace does not hold yet.
// It is checked against everything stored, not only the withdrawable subset, so
// an edge to a BigQuery table or a bucket is not upserted again on every run.
func missingRelationships(desired, stored []*entitiescustomv1.Relationship) []*entitiescustomv1.Relationship {
	storedSet := make(map[string]struct{}, len(stored))
	for _, rel := range stored {
		storedSet[rel.String()] = struct{}{}
	}

	var missing []*entitiescustomv1.Relationship
	for _, rel := range desired {
		if _, exists := storedSet[rel.String()]; !exists {
			missing = append(missing, rel)
		}
	}
	return missing
}

// staleRelationships returns the withdrawable edges this run did not compute:
// the subscription behind them is gone from Pub/Sub, so the edge is drift.
//
// A run that computed nothing withdraws nothing. Relationships are opt-in, so the
// desired set is empty on a plain run, and treating that as "everything stored is
// unwanted" is how this sync deleted another integration's edges.
func staleRelationships(desired, withdrawable []*entitiescustomv1.Relationship) []*entitiescustomv1.Relationship {
	if len(desired) == 0 {
		return nil
	}

	desiredSet := make(map[string]struct{}, len(desired))
	for _, rel := range desired {
		desiredSet[rel.String()] = struct{}{}
	}

	var stale []*entitiescustomv1.Relationship
	for _, rel := range withdrawable {
		if _, exists := desiredSet[rel.String()]; !exists {
			stale = append(stale, rel)
		}
	}
	return stale
}

// ownsRelationship reports whether this integration is the producer of rel: an
// edge from a topic to one of its own subscriptions, which is the only shape it
// publishes between two Pub/Sub entities.
//
// A subscription's entity id is its topic's id plus the subscription name, so
// the test is exactly that. Everything else the workspace holds around a topic —
// a service catalog linking a consumer, a bucket's notification edge — belongs to
// another producer, and withdrawing it makes every run undo their work.
func ownsRelationship(rel *entitiescustomv1.Relationship) bool {
	topic := rel.Upstream.GetCustom().GetId()
	if !strings.HasPrefix(topic, "pubsub::") {
		return false
	}
	return strings.HasPrefix(rel.Downstream.GetCustom().GetId(), topic+"::")
}

// withdrawableRelationships keeps the stored edges this run is answerable for:
// ones it is the producer of, between a topic it scanned and a subscription its
// configuration would have published had that subscription still existed.
//
// The subscription end is judged by the filter rather than by whether the run
// inventoried an entity for it, because the two reasons a subscription has no
// entity are opposites. Excluded by configuration means the edge is not this
// run's to touch; deleted from Pub/Sub means the edge is exactly what this run
// exists to withdraw.
func withdrawableRelationships(
	rels []*entitiescustomv1.Relationship,
	acceptedTopicIds map[string]bool,
	subscriptionFilter Filter,
) []*entitiescustomv1.Relationship {
	var withdrawable []*entitiescustomv1.Relationship
	for _, rel := range rels {
		if !ownsRelationship(rel) {
			continue
		}
		topicEntity := rel.Upstream.GetCustom().GetId()
		if !acceptedTopicIds[strings.TrimPrefix(topicEntity, "pubsub::")] {
			continue
		}
		subscription := strings.TrimPrefix(rel.Downstream.GetCustom().GetId(), topicEntity+"::")
		if !subscriptionFilter.Accept(subscription) {
			continue
		}
		withdrawable = append(withdrawable, rel)
	}
	return withdrawable
}

// ============================================================================
// Entity Group Management
// ============================================================================

// updateEntityGroup updates the entity group for automatic cleanup
func updateEntityGroup(
	ctx context.Context,
	cfg *config.Config,
	client entitiescustomv1grpc.GroupsServiceClient,
	createdEntities []*entitiesv1.Identifier,
) {
	logger := slog.Default()

	logger.InfoContext(ctx, "Updating entity group", slog.String("group_id", cfg.GCP.EntityGroupID))

	_, err := client.UpsertEntitiesGroup(ctx, &entitiescustomv1.UpsertEntitiesGroupRequest{
		Group: &entitiescustomv1.Group{
			GroupId:   cfg.GCP.EntityGroupID,
			EntityIds: createdEntities,
			CreatedAt: timestamppb.Now(),
			UpdatedAt: timestamppb.Now(),
		},
	})
	if err != nil {
		logger.ErrorContext(ctx, "Failed to update entity group", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// ============================================================================
// Logging Setup
// ============================================================================

// setupLogging configures slog based on environment variables
func setupLogging(ctx context.Context) {
	// Determine if source location should be added (defaults to false for cleaner output)
	addSource := false
	if logAddSource := os.Getenv("LOG_ADD_SOURCE"); logAddSource == "true" {
		addSource = true
	}

	// Determine log level from environment variable (defaults to INFO)
	logLevel := slog.LevelInfo
	if logLevelStr := os.Getenv("LOG_LEVEL"); logLevelStr != "" {
		switch logLevelStr {
		case "DEBUG":
			logLevel = slog.LevelDebug
		case "INFO":
			logLevel = slog.LevelInfo
		case "WARN":
			logLevel = slog.LevelWarn
		case "ERROR":
			logLevel = slog.LevelError
		default:
			slog.Default().WarnContext(ctx, "Invalid LOG_LEVEL value, using INFO", slog.String("value", logLevelStr))
		}
	}

	// Configure handler options
	handlerOpts := &slog.HandlerOptions{
		AddSource: addSource,
		Level:     logLevel,
	}

	// Determine log format from environment variable (defaults to text for human readability)
	logFormat := os.Getenv("LOG_FORMAT")
	var handler slog.Handler
	if logFormat == "json" {
		handler = slog.NewJSONHandler(os.Stdout, handlerOpts)
	} else {
		// Default to text format for better human readability
		handler = slog.NewTextHandler(os.Stdout, handlerOpts)
	}

	slog.SetDefault(slog.New(handler))
}

// ============================================================================
// Utility Functions
// ============================================================================

// checkCancellation checks if context is cancelled and returns early if so
func checkCancellation(ctx context.Context) error {
	select {
	case <-ctx.Done():
		logger := slog.Default()
		logger.InfoContext(ctx, "Context cancelled, stopping scan")
		return ctx.Err()
	default:
		return nil
	}
}

// truncate truncates a string to a maximum length
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return s
}

// listCustomEntities retrieves all custom entities and returns them as a map for efficient lookups
func listCustomEntities(ctx context.Context, client entitiescustomv1grpc.EntitiesServiceClient) (map[string]bool, error) {
	if client == nil {
		return nil, nil // In dry-run mode, return nil
	}

	resp, err := client.ListEntities(ctx, &entitiescustomv1.ListEntitiesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list entities: %w", err)
	}

	entityMap := make(map[string]bool)
	for _, entity := range resp.GetEntities() {
		if customID := entity.GetId().GetCustom(); customID != nil {
			entityMap[customID.GetId()] = true
		}
	}

	return entityMap, nil
}
