# integration-google-cloud-pubsub

Integration which scrapes Google Cloud Pub/Sub topics and subscriptions as custom entities in the SYNQ platform.

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

The application works with sensible defaults and minimal configuration. Only SYNQ API credentials are required.

### Required: Environment Variables (.env)

Create a `.env` file in the project root with your SYNQ API credentials:

```bash
SYNQ_CLIENT_ID=your_client_id_here
SYNQ_CLIENT_SECRET=your_client_secret_here

# GCP Project ID (optional if running on GCP or using gcloud)
GCP_PROJECT_ID=your-gcp-project-id
```

See `.env.example` for a template.

**Note:** The GCP project ID can be auto-detected from:
- `GOOGLE_CLOUD_PROJECT` environment variable
- `GCLOUD_PROJECT` environment variable (legacy)
- GCP metadata server (when running on GCP)
- `gcp.project_id` in config.yaml

If not set via any of these methods, you must provide `GCP_PROJECT_ID`.

### Optional: Configuration File (config.yaml)

The application works with defaults out of the box. For customization, create a `config.yaml` file (see `config.yaml.example` for reference):

**Configuration precedence:** defaults → config.yaml → environment variables

```yaml
# SYNQ API Configuration (optional, defaults shown for EU region)
synq:
  endpoint: "developer.synq.io:443"  # EU region (default)
  # For US region, use: "api.us.synq.io:443"
  oauth_url: "https://developer.synq.io/oauth2/token"  # EU region (default)
  # For US region, use: "https://api.us.synq.io/oauth2/token"

# GCP Configuration (optional, defaults shown)
gcp:
  user_agent: "synq-pubsub-client-v1.0.0"
  # project_id can also be set here instead of GCP_PROJECT_ID env var
  # entity_group_id: "pubsub::custom-group-id"  # defaults to pubsub::<project_id>

# Custom Entity Type IDs (optional, defaults shown)
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

**Defaults:**
- SYNQ endpoint: `developer.synq.io:443` (EU region - for US region use `api.us.synq.io:443`)
- OAuth URL: `https://developer.synq.io/oauth2/token` (EU region - for US region use `https://api.us.synq.io/oauth2/token`)
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

If your GCP project has firewall rules that restrict inbound connections, you may need to whitelist SYNQ's egress IP addresses to allow the integration to access your Pub/Sub resources.

### SYNQ Egress IP Addresses

Whitelist the following IP addresses based on your SYNQ deployment region:

**EU Region (Default)**
- App: https://app.synq.io
- API: https://developer.synq.io
- **Egress IP: `34.105.135.39`**

**US Region**
- App: https://app.us.synq.io
- API: https://api.us.synq.io
- **Egress IP: `35.238.250.82`**

For the latest IP addresses, see the [SYNQ Security Documentation](https://docs.synq.io/security/ip#ip-addresses-by-region).

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
- `--gcp.project-id` - GCP project ID (auto-detected if not set)
- `--synq.client-id` - SYNQ API client ID (or use SYNQ_CLIENT_ID env var)
- `--synq.client-secret` - SYNQ API client secret (or use SYNQ_CLIENT_SECRET env var)

**Filter flags:**
- `--filter.topics.include` - Topic name patterns to include
- `--filter.topics.exclude` - Topic name patterns to exclude
- `--filter.subscriptions.include` - Subscription name patterns to include
- `--filter.subscriptions.exclude` - Subscription name patterns to exclude

**Relationship flags:**
- `--relationships.enabled` - Enable topic->subscription relationships (default: false)
- `--relationships.filter.include` - Relationship patterns to include
- `--relationships.filter.exclude` - Relationship patterns to exclude

**Type configuration flags:**
- `--types.topic-type-id` - SYNQ entity type ID for topics (default: 20)
- `--types.subscription-type-id` - SYNQ entity type ID for subscriptions (default: 21)
- `--types.topic-icon` - Path to custom topic icon SVG
- `--types.subscription-icon` - Path to custom subscription icon SVG

**Advanced flags:**
- `--gcp.entity-group-id` - Entity group ID (defaults to `pubsub::<project_id>`)
- `--gcp.user-agent` - User agent for GCP API calls
- `--synq.endpoint` - SYNQ API endpoint (EU: `developer.synq.io:443`, US: `api.us.synq.io:443`)
- `--synq.oauth-url` - SYNQ OAuth2 token URL (EU: `https://developer.synq.io/oauth2/token`, US: `https://api.us.synq.io/oauth2/token`)

Run `go run main.go --help` to see all available flags.

### Examples

```bash
# Run with custom project ID
go run main.go --gcp.project-id=my-project

# Run with US region endpoints
go run main.go --synq.endpoint=api.us.synq.io:443 --synq.oauth-url=https://api.us.synq.io/oauth2/token

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

1. Authenticates with SYNQ API using OAuth2 client credentials
2. Creates/updates custom entity types (Topic and Subscription)
3. Iterates through GCP Pub/Sub topics and subscriptions
4. Creates entities in SYNQ for each resource
5. Manages relationships between topics and subscriptions
6. Uses entity groups to track resources by project (enables automatic cleanup)

### Architecture Overview

The integration consists of three main components:

**main.go** - Main application entry point:
- Configuration management using viper (supports config file, env vars, and CLI flags)
- Client setup (SYNQ gRPC with OAuth2, GCP Pub/Sub)
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

**Entity Groups:** The integration uses entity groups to track all entities created in each run. When the group is updated, SYNQ automatically removes entities that were in the previous group but not in the current one, enabling automatic cleanup of deleted resources.

**Relationship Management:** When enabled, the integration creates relationships between topics and their subscriptions. The system deduplicates relationships to avoid recreating existing ones and cleans up relationships that no longer exist.

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

- `buf.build/gen/go/getsynq/api` - SYNQ API protocol buffers (gRPC and protobuf)
- `cloud.google.com/go/pubsub` - Google Cloud Pub/Sub client
- `golang.org/x/oauth2/clientcredentials` - OAuth2 client credentials flow
- `github.com/spf13/cobra` - CLI framework
- `github.com/spf13/viper` - Configuration management
- `github.com/stretchr/testify` - Testing framework with suite support

See `go.mod` for the complete list of dependencies.
