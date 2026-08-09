package local

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestAWSProfileRefusesInCI(t *testing.T) {
	t.Setenv("CI", "true")
	p := &AWSProfile{ProviderName: "aws-local", Profile: "dev"}
	_, err := p.Acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refuses") {
		t.Fatalf("expected CI refusal, got %v", err)
	}
}

func TestAWSProfileOKWhenNotCI(t *testing.T) {
	t.Setenv("CI", "")
	p := &AWSProfile{ProviderName: "aws-local", Profile: "dev", Region: "us-west-2"}
	c, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Env["AWS_PROFILE"] != "dev" || c.Env["AWS_REGION"] != "us-west-2" {
		t.Fatalf("env wrong: %+v", c.Env)
	}
}

func TestEnvPassthroughRefusesWithoutFlag(t *testing.T) {
	p := &EnvPassthrough{ProviderName: "leak"}
	_, err := p.Acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "dangerous") {
		t.Fatalf("expected refusal without i_understand: %v", err)
	}
}

func TestEnvPassthroughCopiesVars(t *testing.T) {
	t.Setenv("MY_SECRET", "hush")
	p := &EnvPassthrough{
		ProviderName: "passthrough",
		IUnderstand:  true,
		EnvVars:      map[string]string{"MY_SECRET_OUT": "MY_SECRET"},
	}
	c, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Env["MY_SECRET_OUT"] != "hush" {
		t.Fatalf("passthrough failed: %+v", c.Env)
	}
}

// TestEnvPassthroughSkipsUnsetHostVars pins that an unset host variable is
// not exported as an empty string. An empty value looks to the engine like
// a configured-but-blank credential, so the failure surfaced as a confusing
// auth rejection from the cloud provider instead of "you did not set this".
func TestEnvPassthroughSkipsUnsetHostVars(t *testing.T) {
	t.Setenv("REEVE_TEST_PRESENT", "value-here")
	if err := os.Unsetenv("REEVE_TEST_ABSENT"); err != nil {
		t.Fatal(err)
	}

	p := &EnvPassthrough{
		ProviderName: "danger",
		IUnderstand:  true,
		EnvVars: map[string]string{
			"AWS_ACCESS_KEY_ID":     "REEVE_TEST_PRESENT",
			"AWS_SECRET_ACCESS_KEY": "REEVE_TEST_ABSENT",
		},
	}
	cred, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := cred.Env["AWS_ACCESS_KEY_ID"]; got != "value-here" {
		t.Errorf("present var = %q, want %q", got, "value-here")
	}
	if _, ok := cred.Env["AWS_SECRET_ACCESS_KEY"]; ok {
		t.Error("unset host var was exported; it must be omitted so the engine sees it as absent, not blank")
	}
}

// TestEnvPassthroughExportsExplicitlyEmptyHostVars pins the other half of
// the contract: an unset variable is skipped, but one deliberately set to
// the empty string is a value the operator chose and is passed through.
func TestEnvPassthroughExportsExplicitlyEmptyHostVars(t *testing.T) {
	t.Setenv("REEVE_TEST_EMPTY", "")

	p := &EnvPassthrough{
		ProviderName: "danger",
		IUnderstand:  true,
		EnvVars:      map[string]string{"SOME_KEY": "REEVE_TEST_EMPTY"},
	}
	cred, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	v, ok := cred.Env["SOME_KEY"]
	if !ok {
		t.Fatal("an explicitly empty host var was skipped; only UNSET vars should be")
	}
	if v != "" {
		t.Fatalf("value = %q, want empty", v)
	}
}
