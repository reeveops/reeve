package schemas

// Auth is .reeve/auth.yaml. Providers declare named credential sources;
// Bindings map stack patterns to sets of providers. See
// openspec/specs/auth for semantics.
type Auth struct {
	Header    `yaml:",inline"`
	Providers map[string]ProviderYAML `yaml:"providers"`
	Bindings  []BindingYAML           `yaml:"bindings"`
}

// ProviderYAML is a named provider declaration. The Type determines
// which adapter is constructed; all other fields are type-specific and
// parsed on demand by the adapter.
//
// Fields tagged `expand:"env"` are on the enumerated env-expansion
// allow-list (docs/configuration.md#token-expansion); every other field
// keeps `${env:...}` literal.
type ProviderYAML struct {
	Type string `yaml:"type"`

	// Cloud federation
	RoleARN                  string `yaml:"role_arn,omitempty"`
	SessionName              string `yaml:"session_name,omitempty"`
	Duration                 string `yaml:"duration,omitempty"`
	Region                   string `yaml:"region,omitempty"`
	WorkloadIdentityProvider string `yaml:"workload_identity_provider,omitempty"`
	ServiceAccount           string `yaml:"service_account,omitempty"`
	TenantID                 string `yaml:"tenant_id,omitempty" expand:"env"`
	ClientID                 string `yaml:"client_id,omitempty" expand:"env"`
	SubscriptionID           string `yaml:"subscription_id,omitempty" expand:"env"`
	AudienceOverride         string `yaml:"audience,omitempty"`

	// Secret managers
	Source     string `yaml:"source,omitempty"`     // name of the parent cloud-federation provider
	SecretID   string `yaml:"secret_id,omitempty"`  // AWS Secrets Manager
	Parameter  string `yaml:"parameter,omitempty"`  // AWS SSM
	GCPName    string `yaml:"name,omitempty"`       // gcp_secret_manager: full resource name
	VaultName  string `yaml:"vault_name,omitempty"` // azure_key_vault
	SecretName string `yaml:"secret_name,omitempty"`

	// EnvMap maps env var name → field inside the fetched secret and is
	// REQUIRED for every secret-manager provider (lint errors without it:
	// the provider exports nothing). An empty field value ("") exports the
	// whole secret and is only valid when the secret is a plain string -
	// never a JSON bundle. A named field missing from the secret is a hard
	// error at acquire time (fail closed).
	EnvMap map[string]string `yaml:"env_map,omitempty"`

	TTL string `yaml:"ttl,omitempty"` // secret cache TTL

	// GitHub App
	AppID          any      `yaml:"app_id,omitempty" expand:"env"`          // int or string
	InstallationID any      `yaml:"installation_id,omitempty" expand:"env"` // int or string
	PrivateKey     string   `yaml:"private_key,omitempty" expand:"env"`
	Permissions    []string `yaml:"permissions,omitempty"`

	// Vault
	Address  string `yaml:"address,omitempty"`
	Path     string `yaml:"path,omitempty"`
	AuthType string `yaml:"auth_type,omitempty"`

	// Local dev
	Profile string `yaml:"profile,omitempty"`

	// env_passthrough
	EnvVars                    map[string]string `yaml:"env_vars,omitempty"`
	IUnderstandThisIsDangerous bool              `yaml:"i_understand_this_is_dangerous,omitempty"`

	// GitHub secret env
	EnvVar string `yaml:"env_var,omitempty"`
}

// BindingYAML is one binding entry.
type BindingYAML struct {
	Match     BindingMatch `yaml:"match"`
	Providers []string     `yaml:"providers"`
	Override  []string     `yaml:"override,omitempty"`
}

// BindingMatch selects bindings by stack pattern and optional run mode.
type BindingMatch struct {
	Stack string `yaml:"stack"`
	Mode  string `yaml:"mode,omitempty"` // preview | apply | drift
}
