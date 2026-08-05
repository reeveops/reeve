package schemas

// Observability is .reeve/observability.yaml. Opt-in: off by default. If
// this file does not exist, reeve emits no telemetry at all.
type Observability struct {
	Header      `yaml:",inline"`
	OTEL        OTELConfig         `yaml:"otel"`
	Annotations []AnnotationConfig `yaml:"annotations"`
}

// Fields tagged `expand:"env"` are on the enumerated env-expansion
// allow-list (docs/configuration.md#token-expansion).
type OTELConfig struct {
	Enabled       bool              `yaml:"enabled"`
	Endpoint      string            `yaml:"endpoint" expand:"env"`
	ServiceName   string            `yaml:"service_name"`
	ResourceAttrs map[string]string `yaml:"resource_attributes" expand:"env"`
	// StackCardinality controls the stack label cardinality:
	//   allow | hash | drop (default: hash)
	StackCardinality string `yaml:"stack_cardinality"`
	// Headers are passed to the OTLP exporter (supports ${env:X}).
	Headers map[string]string `yaml:"headers" expand:"env"`
}

type AnnotationConfig struct {
	Type     string            `yaml:"type"` // grafana | dash0 | datadog | webhook
	URL      string            `yaml:"url" expand:"env"`
	Endpoint string            `yaml:"endpoint" expand:"env"` // some systems prefer "endpoint"
	APIKey   string            `yaml:"api_key" expand:"env"`
	Events   []string          `yaml:"events"`
	Headers  map[string]string `yaml:"headers,omitempty" expand:"env"`
}
