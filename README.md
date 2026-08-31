# Coalesce Quality Google Cloud Pub/Sub Integration

Automatically import and track your Google Cloud Pub/Sub topics and subscriptions in the Coalesce Quality data catalog.

## What It Does

This integration:
- Discovers all Pub/Sub topics and subscriptions in your GCP project
- Creates and maintains entities in Coalesce Quality for visibility and governance
- Tracks relationships between topics and subscriptions
- Creates cross-platform lineage to BigQuery tables and Cloud Storage buckets
- Automatically cleans up removed resources
- Supports flexible filtering and customization

**Use cases:** Data catalog management, resource discovery, compliance tracking, cross-platform data lineage.

## Installation

### Download Pre-built Binaries

Download the latest release for your platform from the [releases page](https://github.com/getsynq/synq-google-cloud-pubsub/releases).

**macOS (Intel):**
```bash
curl -LO https://github.com/getsynq/synq-google-cloud-pubsub/releases/latest/download/synq-google-cloud-pubsub_darwin_amd64.tar.gz
tar -xzf synq-google-cloud-pubsub_darwin_amd64.tar.gz
sudo mv synq-google-cloud-pubsub /usr/local/bin/
```

**macOS (Apple Silicon):**
```bash
curl -LO https://github.com/getsynq/synq-google-cloud-pubsub/releases/latest/download/synq-google-cloud-pubsub_darwin_arm64.tar.gz
tar -xzf synq-google-cloud-pubsub_darwin_arm64.tar.gz
sudo mv synq-google-cloud-pubsub /usr/local/bin/
```

**Linux (AMD64):**
```bash
curl -LO https://github.com/getsynq/synq-google-cloud-pubsub/releases/latest/download/synq-google-cloud-pubsub_linux_amd64.tar.gz
tar -xzf synq-google-cloud-pubsub_linux_amd64.tar.gz
sudo mv synq-google-cloud-pubsub /usr/local/bin/
```

**Linux (ARM64):**
```bash
curl -LO https://github.com/getsynq/synq-google-cloud-pubsub/releases/latest/download/synq-google-cloud-pubsub_linux_arm64.tar.gz
tar -xzf synq-google-cloud-pubsub_linux_arm64.tar.gz
sudo mv synq-google-cloud-pubsub /usr/local/bin/
```

**Windows:**
Download the `.zip` file from the releases page and extract it.

### Build from Source

Requires Go 1.24 or later:
```bash
git clone https://github.com/getsynq/synq-google-cloud-pubsub.git
cd synq-google-cloud-pubsub
go build
```

## Configuration

The application works with sensible defaults and minimal configuration. All it needs is a credential and a GCP project.

### Authentication

There are three ways to authenticate, and they resolve in this order — the
unattended ones win, so a scheduled sync never picks up a developer's browser
session:

**1. Client credentials.** For CI and scheduled runs. In the environment or in a
`.env` file in the project root:

```bash
QUALITY_CLIENT_ID=your_client_id_here
QUALITY_CLIENT_SECRET=your_client_secret_here
```

**2. A pre-issued access token**, as `QUALITY_TOKEN`.

**3. A browser login.** For running it by hand:

```bash
synq-google-cloud-pubsub auth login          # opens a browser
synq-google-cloud-pubsub auth status         # what is stored, and for which deployment
synq-google-cloud-pubsub auth logout
```

The credential is cached under `~/.synq/oauth/`, partitioned by deployment, and
**shared with the other Coalesce Quality tools** — so a login done by `synqctl`
or `synq-recon` already serves this integration, and the other way round.

Publishing entities needs `SCOPE_ENTITY_EDIT`, `SCOPE_ENTITY_TYPE_EDIT` and
`SCOPE_LINEAGE_EDIT`, which reach a personal token through the Admin role and
above. An account without them can still run `--dry-run`; `auth status` says
which of your stored credentials can publish.

The `SYNQ_`-prefixed spelling of every variable above is still read, so existing
`.env` files and CI configuration keep working.

See `.env.example` for a template.

### Choosing a deployment

```bash
synq-google-cloud-pubsub --region us          # eu (default), us or au
synq-google-cloud-pubsub --endpoint host:443  # a self-hosted deployment
```

Resolved highest first: `--endpoint`, `--region`, the config file, then
`QUALITY_API_ENDPOINT` / `QUALITY_REGION`, then the deployment you last logged
into, then the EU region. So logging into a single region once is enough; you
never type `--region` again.

**Note:** The GCP project ID can be auto-detected from (in order of precedence):
1. `GCP_PROJECT_ID` environment variable
2. `GOOGLE_CLOUD_PROJECT` environment variable
3. `GCLOUD_PROJECT` environment variable (legacy)
4. `gcloud` CLI configuration (`gcloud config get-value project`)
5. GCP metadata server (when running on GCP)
6. `gcp.project_id` in config.yaml

If your gcloud CLI is configured with a project (`gcloud config set project YOUR_PROJECT`), the application will automatically use it.

### Optional: Configuration File (config.yaml)

The application works with defaults out of the box. For customization, create a `config.yaml` file (see `config.yaml.example` for reference):

**Configuration precedence:** defaults → config.yaml → environment variables

```yaml
# Coalesce Quality configuration (all optional)
quality:
  # region: "us"                      # eu (default), us or au
  # endpoint: "api.us.synq.io:443"    # overrides region
  # client_id: ""                     # prefer QUALITY_CLIENT_ID
  # client_secret: ""                 # prefer QUALITY_CLIENT_SECRET
  # token: ""                         # a pre-issued access token
# The `synq:` section this replaced is still read, and fills in anything
# `quality:` leaves unset.

# GCP Configuration (optional, defaults shown)
gcp:
  user_agent: "synq-pubsub-client-v1.0.0"
  # project_id can also be set here instead of GCP_PROJECT_ID env var
  # entity_group_id: "pubsub::custom-group-id"  # defaults to pubsub::<project_id>

# Custom entity type IDs (optional, defaults shown)
types:
  topic_type_id: 20
  subscription_type_id: 21
  # Optional: custom icons
  # topic_icon: "path/to/custom-topic-icon.svg"
  # subscription_icon: "path/to/custom-subscription-icon.svg"

# Resource Filters (optional)
filter:
  topics:
    include: []  # Empty means include all
    exclude: []  # Regex patterns to exclude

  subscriptions:
    include: []  # Empty means include all
    exclude:
      # Exclude auto-generated per-pod subscriptions
      - '-[a-z0-9]{9,10}-[a-z0-9]{5}\.subscription$'

# Relationship Management (disabled by default)
relationships:
  enabled: false  # Set to true to enable topic->subscription relationships
  filter:
    include: []  # Empty means include all (format: "topic_id->subscription_id")
    exclude: []  # Regex patterns to exclude relationship pairs
    # Examples:
    # include: ["important-topic->.*"]  # Only create relationships for important-topic
    # exclude: ["test-.*->.*"]          # Skip relationships for test topics
```

### Cross-Platform Lineage

The integration automatically creates lineage relationships when subscriptions deliver messages to:
- **BigQuery tables** (via `BigQueryConfig`) - creates relationships to native BigQuery table entities
- **Cloud Storage buckets** (via `CloudStorageConfig`) - creates relationships to custom GCS bucket entities

**Requirements:**
- **BigQuery lineage**: a native BigQuery integration in Coalesce Quality. Links to non-existent tables are created anyway (safe).
- **Cloud Storage lineage**: [GCS integration](https://github.com/getsynq/synq-google-cloud-storage) should be set up first. Links to non-existent `gcs::<bucket_name>` entities are skipped with debug logging.

**Behavior:**
- **BigQuery relationships**: Always created (non-custom entities are safe to link)
- **GCS relationships**: Only created if the `gcs::<bucket_name>` entity exists in Coalesce Quality
  - If GCS bucket entity doesn't exist, relationship is skipped with a debug log message
  - No sync failures - relationships are created opportunistically

**Optional - Excluding Cloud Storage relationships:**

While not required (missing entities are safely skipped), you can explicitly exclude GCS relationships if desired:

```yaml
relationships:
  enabled: true
  filter:
    exclude:
      # Optional: Explicitly exclude GCS relationships
      - '->gcs-.*'

      # Optional: Exclude BigQuery relationships
      # - '->bq-.*'
```

**Defaults:**
- Deployment: the EU region, or the one you last logged into. `--region us` / `--region au` select the others, and every OAuth endpoint is discovered from the deployment itself.
- Type IDs: Topic=20, Subscription=21
- User agent: `synq-pubsub-client-v1.0.0`
- Entity group ID: `pubsub::<project_id>` (for automatic cleanup of removed resources)
- Relationships: **disabled** (set `relationships.enabled: true` to enable)
- Icons: Embedded SVG from `icons/pubsub.svg`

### Logging Configuration

The application uses structured logging (slog) configured via environment variables:

```bash
# Log level (default: INFO)
LOG_LEVEL=DEBUG    # Options: DEBUG, INFO, WARN, ERROR

# Log format (default: text)
LOG_FORMAT=text    # Options: text, json

# Add source code location to logs (default: false)
LOG_ADD_SOURCE=true
```

Example with different log levels:
```bash
# Debug mode - shows all details including filtered resources
LOG_LEVEL=DEBUG go run main.go

# Production mode - JSON format for log aggregation
LOG_FORMAT=json LOG_LEVEL=INFO go run main.go

# Minimal output
LOG_LEVEL=WARN go run main.go
```

## Network Requirements

If your GCP project has firewall rules that restrict inbound connections, you may need to whitelist the Coalesce Quality egress IP addresses to allow the integration to access your Pub/Sub resources.

### Egress IP addresses

Whitelist the following IP addresses based on your deployment region:

**EU Region (Default)**
- App: https://app.synq.io
- API: https://developer.synq.io
- **Egress IP: `34.105.135.39`**

**US Region**
- App: https://app.us.synq.io
- API: https://api.us.synq.io
- **Egress IP: `35.238.250.82`**

For the latest IP addresses, see the [security documentation](https://docs.synq.io/security/ip#ip-addresses-by-region).

## Running

The application requires only a `.env` file with credentials. No `config.yaml` needed for basic usage:

```bash
# Minimal setup: just create .env with credentials
cp .env.example .env
# Edit .env and add your credentials

# Run with defaults
go run main.go

# Run with debug logging
LOG_LEVEL=DEBUG go run main.go

# Run with JSON logging for production
LOG_FORMAT=json go run main.go

# View all available flags
go run main.go --help
```

The application supports graceful shutdown with Ctrl-C.

### Command-Line Flags

All configuration options are available as command-line flags. Flags have the highest precedence (override config file and environment variables).

**Common flags:**
- `-c, --config` - Path to config file (default: `config.yaml`)
- `-h, --help` - Show help message
- `--dry-run` - Dry-run mode: scan GCP resources but don't call the Coalesce Quality API
- `--gcp.project-id` - GCP project ID (auto-detected if not set)
- `--client-id` - client credential id (or `QUALITY_CLIENT_ID`)
- `--client-secret` - client credential secret (or `QUALITY_CLIENT_SECRET`)
- `--region` - deployment: `eu`, `us` or `au`
- `--endpoint` - API endpoint, overriding `--region`

**Filter flags:**
- `--filter.topics.include` - Topic name patterns to include
- `--filter.topics.exclude` - Topic name patterns to exclude
- `--filter.subscriptions.include` - Subscription name patterns to include
- `--filter.subscriptions.exclude` - Subscription name patterns to exclude

**Relationship flags:**
- `--relationships.enabled` - Enable topic->subscription relationships (default: false)
- `--relationships.prune` - Withdraw the topic->subscription relationships this integration published, and create none
- `--relationships.filter.include` - Relationship patterns to include
- `--relationships.filter.exclude` - Relationship patterns to exclude

**Type configuration flags:**
- `--types.topic-type-id` - custom entity type ID for topics (default: 20)
- `--types.subscription-type-id` - custom entity type ID for subscriptions (default: 21)
- `--types.topic-icon` - Path to custom topic icon SVG
- `--types.subscription-icon` - Path to custom subscription icon SVG

**Advanced flags:**
- `--gcp.entity-group-id` - Entity group ID (defaults to `pubsub::<project_id>`)
- `--gcp.user-agent` - User agent for GCP API calls
- `--synq.endpoint`, `--synq.client-id`, `--synq.client-secret`, `--synq.oauth-url` - the names these replaced. Still accepted, hidden from `--help`.

Run `go run main.go --help` to see all available flags.

### Dry-Run Mode

Use `--dry-run` to scan GCP Pub/Sub resources without publishing anything:

```bash
# Dry-run mode (no API calls)
go run main.go --dry-run

# Dry-run with debug logging to see what would be created
LOG_LEVEL=DEBUG go run main.go --dry-run
```

In dry-run mode:
- ✅ Scans GCP Pub/Sub topics and subscriptions
- ✅ Applies filters
- ✅ Shows what entities would be created
- ❌ Does not call the Coalesce Quality API
- ❌ Does not create/update entities or relationships
- ❌ Does not require credentials

### Examples

```bash
# Run with custom project ID
go run main.go --gcp.project-id=my-project

# Run against the US region
go run main.go --region=us

# Run with relationships enabled and custom filters
go run main.go --relationships.enabled --filter.topics.exclude="test-.*"

# Run with custom config file
go run main.go --config=config.production.yaml

# Combine config file with flag overrides
go run main.go --config=config.yaml --relationships.enabled

# Run with custom entity type IDs
go run main.go --types.topic-type-id=100 --types.subscription-type-id=101
```

## How It Works

1. Authenticates against the Coalesce Quality API (browser login, client credentials or a pre-issued token)
2. Creates/updates custom entity types (Topic and Subscription)
3. Iterates through GCP Pub/Sub topics and subscriptions
4. Creates an entity for each resource
5. Manages relationships between topics and subscriptions
6. Uses entity groups to track resources by project (enables automatic cleanup)

### Architecture Overview

The integration consists of three main components:

**main.go** - Main application entry point:
- Configuration management using viper (supports config file, env vars, and CLI flags)
- Client setup (Coalesce Quality gRPC, GCP Pub/Sub)
- Resource synchronization orchestration
- Graceful shutdown handling

**config/config.go** - Configuration management:
- Multi-source configuration loading with proper precedence
- Auto-detection of GCP project ID from multiple sources
- Validation of required fields

**filter.go** - Resource filtering system:
- `IncludeExcludeFilter` - Combines multiple filters with include/exclude logic
- `RegexFilter` - Matches strings against regex patterns
- Default filter excludes auto-generated per-pod subscriptions

### Key Features

**Entity Groups:** The integration uses entity groups to track all entities created in each run. When the group is updated, Coalesce Quality automatically removes entities that were in the previous group but not in the current one, enabling automatic cleanup of deleted resources.

**Relationship Management:** When enabled, the integration creates relationships between topics and their subscriptions, and withdraws the ones that no longer exist.

**Relationships are off by default on purpose.** Linking a topic to its
subscriptions closes a cycle for every service that consumes a topic it also
publishes, and a catalog full of two-hop cycles is harder to follow than one
where the publishing and consuming sides are separate. `--relationships.prune`
is the way back if a workspace turned them on: it withdraws the edges this
integration published and creates none.

A run only ever withdraws relationships **it computed itself**:

- Relationships are off by default, and a run with them off withdraws none. With them on, a topic whose last subscription was deleted does lose that edge: reconciling is what the feature is for.
- Only an edge from a topic to one of *its own* subscriptions belongs to this integration. A service catalog's edge into a topic, or a bucket's notification edge, is another producer's and is left alone.
- Only edges between a topic this run scanned and a subscription its filters accept are judged, so a subscription excluded by a filter — or a relationship excluded by `relationships.filter` — keeps its lineage instead of reading as removed. A subscription that is simply gone from Pub/Sub still has its edge withdrawn: that is the reconciliation the feature is for.

**Cycles:** each integration only withdraws the relationship shape it publishes, so the two never undo each other. With both relationship features enabled, a bucket that notifies a topic whose subscription writes back to that same bucket forms a three-hop cycle in the graph. That is a faithful picture of the delivery path rather than a fault, but it is worth knowing before you read it as one.

**Custom Identifiers:** All entities use custom identifiers with `pubsub::` prefix for namespace isolation. Subscriptions use composite identifiers: `pubsub::<topic_id>::<subscription_id>`.

## Development

### Running Tests

```bash
# Run all tests
go test ./...

# Run a specific test
go test -v -run TestFilterSuite

# Run tests with coverage
go test -v -cover ./...
```

### Dependencies

Key dependencies used by this project:

- `buf.build/gen/go/getsynq/api` - Coalesce Quality API protocol buffers (gRPC and protobuf)
- `cloud.google.com/go/pubsub` - Google Cloud Pub/Sub client
- `github.com/getsynq/quality-oauth-go` - the shared browser login and credential store
- `github.com/spf13/cobra` - CLI framework
- `github.com/spf13/viper` - Configuration management
- `github.com/stretchr/testify` - Testing framework with suite support

See `go.mod` for the complete list of dependencies.
