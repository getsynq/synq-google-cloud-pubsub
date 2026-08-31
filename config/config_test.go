package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// TestQualitySectionIsRead covers the promoted spelling of the section.
func TestQualitySectionIsRead(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `
gcp:
  project_id: example-project
quality:
  region: us
  client_id: promoted-id
  client_secret: promoted-secret
`))
	require.NoError(t, err)
	assert.Equal(t, "us", cfg.Quality.Region)
	assert.Equal(t, "promoted-id", cfg.Quality.ClientID)
	assert.Empty(t, cfg.DeprecatedKeys)
}

// TestSynqSectionIsStillRead is the compatibility case: the section this one
// replaced is in existing config files, so it keeps working and is named in the
// warning rather than silently ignored.
func TestSynqSectionIsStillRead(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `
gcp:
  project_id: example-project
synq:
  endpoint: api.us.synq.io:443
  client_id: legacy-id
  client_secret: legacy-secret
`))
	require.NoError(t, err)
	assert.Equal(t, "api.us.synq.io:443", cfg.Quality.Endpoint)
	assert.Equal(t, "legacy-id", cfg.Quality.ClientID)
	assert.ElementsMatch(t,
		[]string{"synq.endpoint", "synq.client_id", "synq.client_secret"},
		cfg.DeprecatedKeys)
}

// TestQualitySectionWinsOverSynq pins which way the merge runs. Getting it
// backwards would let a stale section in an old config file override the one the
// user just wrote.
func TestQualitySectionWinsOverSynq(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `
gcp:
  project_id: example-project
quality:
  endpoint: api.us.synq.io:443
synq:
  endpoint: developer.synq.io:443
  client_id: legacy-id
`))
	require.NoError(t, err)
	assert.Equal(t, "api.us.synq.io:443", cfg.Quality.Endpoint)
	// The field the promoted section left empty still comes from the older one.
	assert.Equal(t, "legacy-id", cfg.Quality.ClientID)
	assert.Equal(t, []string{"synq.client_id"}, cfg.DeprecatedKeys)
}

// TestMergeLeavesPopulatedFieldsAlone is the unit behind those two.
func TestMergeLeavesPopulatedFieldsAlone(t *testing.T) {
	q := QualityConfig{Endpoint: "api.us.synq.io:443"}
	used := q.merge(QualityConfig{Endpoint: "developer.synq.io:443", Token: "legacy-token"})

	assert.Equal(t, "api.us.synq.io:443", q.Endpoint)
	assert.Equal(t, "legacy-token", q.Token)
	assert.Equal(t, []string{"synq.token"}, used)
}

// TestCredentialsAreNotRequired keeps the config layer out of a decision it
// cannot make: a run can be authenticated by the environment or by a browser
// login in the shared store, neither of which is visible here.
func TestCredentialsAreNotRequired(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `
gcp:
  project_id: example-project
`))
	require.NoError(t, err)
	assert.Empty(t, cfg.Quality.ClientID)
	assert.Empty(t, cfg.Quality.Endpoint, "no endpoint default here; the deployment is resolved by the auth library")
}

// TestProjectIDIsStillRequired guards the one thing this tool cannot work out on
// its own when nothing in the environment supplies it.
func TestProjectIDIsStillRequired(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("CLOUDSDK_CONFIG", t.TempDir())
	t.Setenv("PATH", "")

	cfg, err := LoadConfig(writeConfig(t, "quality:\n  region: eu\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GCP project ID is required")

	// The error is returned alongside what was read, not instead of it. Only a
	// sync needs a project; `auth login` needs the deployment and nothing else.
	require.NotNil(t, cfg)
	assert.Equal(t, "eu", cfg.Quality.Region)
}

// withFlags gives the test its own flag set, registered the way main does, and
// parses args into it. LoadConfig reads pflag.CommandLine directly, so a test
// that wants to exercise a flag has to stand one up.
func withFlags(t *testing.T, args ...string) {
	t.Helper()
	saved := pflag.CommandLine
	pflag.CommandLine = pflag.NewFlagSet(t.Name(), pflag.ContinueOnError)
	t.Cleanup(func() { pflag.CommandLine = saved })
	InitFlags()
	require.NoError(t, pflag.CommandLine.Parse(args))
}

// TestDeprecatedCredentialFlagsAreStillRead is the compatibility case for the
// flags existing CI passes. They are accepted and hidden rather than removed,
// which is only worth anything if the value they carry actually arrives: viper
// keys the flag as `synq.client-id` while the field wants `synq.client_id`, so
// the value went nowhere and the run failed to authenticate.
func TestDeprecatedCredentialFlagsAreStillRead(t *testing.T) {
	withFlags(t, "--synq.client-id=legacy-id", "--synq.client-secret=legacy-secret")

	cfg, err := LoadConfig(writeConfig(t, "gcp:\n  project_id: example-project\n"))
	require.NoError(t, err)
	assert.Equal(t, "legacy-id", cfg.Quality.ClientID)
	assert.Equal(t, "legacy-secret", cfg.Quality.ClientSecret)
}

// TestPromotedCredentialFlagsWinOverDeprecated pins which way round the pair
// resolves when a script passes both.
func TestPromotedCredentialFlagsWinOverDeprecated(t *testing.T) {
	withFlags(t,
		"--client-id=promoted-id", "--synq.client-id=legacy-id",
		"--client-secret=promoted-secret", "--synq.client-secret=legacy-secret",
	)

	cfg, err := LoadConfig(writeConfig(t, "gcp:\n  project_id: example-project\n"))
	require.NoError(t, err)
	assert.Equal(t, "promoted-id", cfg.Quality.ClientID)
	assert.Equal(t, "promoted-secret", cfg.Quality.ClientSecret)
}

// TestDeprecatedEndpointFlagOutranksTheConfigFile keeps the tier the flag sits
// in: what the user typed beats what the file holds, deprecated spelling or not.
func TestDeprecatedEndpointFlagOutranksTheConfigFile(t *testing.T) {
	withFlags(t, "--synq.endpoint=typed.synq.io:443")

	cfg, err := LoadConfig(writeConfig(t, `
gcp:
  project_id: example-project
quality:
  endpoint: from-file.synq.io:443
`))
	require.NoError(t, err)
	assert.Equal(t, "typed.synq.io:443", cfg.Quality.EndpointFlag)
}
