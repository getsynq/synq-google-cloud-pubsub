package main

import (
	"os"
	"path/filepath"
	"testing"

	qualityoauth "github.com/getsynq/quality-oauth-go"
	"github.com/getsynq/synq-google-cloud-pubsub/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearDeploymentEnv keeps a developer's own shell out of the precedence tests.
func clearDeploymentEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"QUALITY_API_ENDPOINT", "SYNQ_API_ENDPOINT",
		"QUALITY_REGION", "SYNQ_REGION",
	} {
		t.Setenv(name, "")
	}
	// The remembered deployment of the last login is read from the store, which
	// is the tier directly below the environment.
	t.Setenv("QUALITY_HOME", t.TempDir())
}

func TestRegionFromTheConfigFileResolves(t *testing.T) {
	clearDeploymentEnv(t)

	target, err := resolveTarget(&config.Config{Quality: config.QualityConfig{Region: "us"}})
	require.NoError(t, err)
	assert.Equal(t, "us", target.Region)
}

// TestEndpointFlagOutranksTheConfigFile pins the tier the flags sit in. The file
// is offered as the configured endpoint, which deliberately loses to what the
// user typed.
func TestEndpointFlagOutranksTheConfigFile(t *testing.T) {
	clearDeploymentEnv(t)

	target, err := resolveTarget(&config.Config{Quality: config.QualityConfig{
		Region:       "eu",
		EndpointFlag: "api.au.synq.io:443",
	}})
	require.NoError(t, err)
	assert.Equal(t, "api.au.synq.io:443", target.Endpoint)
}

// TestTheConfigFileOutranksTheEnvironment records the ordering rather than
// choosing it: the library places explicit configuration above the ambient
// environment and both below the flags, and every Coalesce Quality tool resolves
// a deployment through it. A config file that names a deployment therefore wins
// over an exported QUALITY_REGION, and only what the user typed wins over the
// file.
func TestTheConfigFileOutranksTheEnvironment(t *testing.T) {
	clearDeploymentEnv(t)
	t.Setenv("QUALITY_REGION", "au")

	target, err := resolveTarget(&config.Config{Quality: config.QualityConfig{Region: "eu"}})
	require.NoError(t, err)
	assert.Equal(t, "eu", target.Region)

	// Nothing in the file, so the environment decides.
	target, err = resolveTarget(&config.Config{})
	require.NoError(t, err)
	assert.Equal(t, "au", target.Region)

	// A flag beats both.
	target, err = resolveTarget(&config.Config{Quality: config.QualityConfig{
		Region:     "eu",
		RegionFlag: "us",
	}})
	require.NoError(t, err)
	assert.Equal(t, "us", target.Region)
}

// TestConfigFileEndpointBeatsItsOwnRegion keeps the two file settings ordered the
// same way the flags are: the more specific one wins.
func TestConfigFileEndpointBeatsItsOwnRegion(t *testing.T) {
	clearDeploymentEnv(t)

	target, err := resolveTarget(&config.Config{Quality: config.QualityConfig{
		Region:   "eu",
		Endpoint: "api.us.synq.io:443",
	}})
	require.NoError(t, err)
	assert.Equal(t, "api.us.synq.io:443", target.Endpoint)
}

func TestAnUnknownRegionInTheConfigFileIsAnError(t *testing.T) {
	clearDeploymentEnv(t)

	_, err := resolveTarget(&config.Config{Quality: config.QualityConfig{Region: "atlantis"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quality.region in the config file")
}

// TestDeclaredScopesCoverWhatASyncWrites keeps the consent screen and the sync in
// step: a scope the tool needs but never declares is one a login cannot grant.
func TestDeclaredScopesCoverWhatASyncWrites(t *testing.T) {
	assert.Empty(t, qualityoauth.MissingScopes(declaredScopes, writeScopes))
}

// TestTheAppRegistersDynamically pins the reason FirstPartyClientID is empty: the
// authorization server seeds a row per released first-party CLI and this is not
// one, so an id it does not know would hang the login waiting for a callback.
func TestTheAppRegistersDynamically(t *testing.T) {
	assert.Empty(t, authApp().FirstPartyClientID)
	assert.Equal(t, toolName, authApp().SoftwareID)
}

// writeConfigFile puts a config file somewhere a subcommand can be pointed at.
func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// noGCPProject puts the process where someone logging in for the first time is:
// nothing in the environment names a project, and there is no gcloud to ask.
func noGCPProject(t *testing.T) {
	t.Helper()
	t.Setenv("GCP_PROJECT_ID", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("CLOUDSDK_CONFIG", t.TempDir())
	t.Setenv("PATH", "")
}

// TestAnUnknownRegionInTheConfigFileDoesNotBlockAFlag is the reproducer for a
// lower tier vetoing a higher one. A typo in the file made --region unusable,
// which is backwards: the flag is the tier that exists to override the file.
func TestAnUnknownRegionInTheConfigFileDoesNotBlockAFlag(t *testing.T) {
	clearDeploymentEnv(t)

	target, err := resolveTarget(&config.Config{Quality: config.QualityConfig{
		Region:     "atlantis",
		RegionFlag: "us",
	}})
	require.NoError(t, err)
	assert.Equal(t, "us", target.Region)

	target, err = resolveTarget(&config.Config{Quality: config.QualityConfig{
		Region:       "atlantis",
		EndpointFlag: "api.us.synq.io:443",
	}})
	require.NoError(t, err)
	assert.Equal(t, "api.us.synq.io:443", target.Endpoint)
}

// TestAuthKeepsTheDeploymentWhenTheGCPProjectIsMissing is the reproducer for
// `auth login --region us` logging in somewhere else.
//
// A sync needs a GCP project and logging in does not, but both read the same
// configuration, so the missing project failed the load and the deployment went
// down with it — flags, config file and all — leaving the default region.
func TestAuthKeepsTheDeploymentWhenTheGCPProjectIsMissing(t *testing.T) {
	clearDeploymentEnv(t)
	noGCPProject(t)

	cmd := &cobra.Command{}
	cmd.Flags().String("config", writeConfigFile(t, "quality:\n  region: us\n"), "")

	target, err := targetFromCommand(cmd)
	require.NoError(t, err)
	assert.Equal(t, "us", target.Region)
}

// TestAnOAuthURLMustBeHTTPS keeps a config file from pointing credentials at a
// server of its choosing. The override exists for a self-hosted authorization
// server, and it is applied to credentials that came from the environment, so a
// config file that names a plain-HTTP host is a way to read them off the wire.
func TestAnOAuthURLMustBeHTTPS(t *testing.T) {
	derived := "https://developer.synq.io/oauth2/token"

	url, err := tokenURL(derived, "")
	require.NoError(t, err)
	assert.Equal(t, derived, url)

	url, err = tokenURL(derived, "https://auth.self-hosted.example/oauth2/token")
	require.NoError(t, err)
	assert.Equal(t, "https://auth.self-hosted.example/oauth2/token", url)

	_, err = tokenURL(derived, "http://evil.example/oauth2/token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "https")

	// Developing against an authorization server on the loopback interface is
	// the one case plain HTTP is not a downgrade.
	url, err = tokenURL(derived, "http://localhost:8080/oauth2/token")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8080/oauth2/token", url)
}
