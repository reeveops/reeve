package annotations

import (
	"github.com/FynxLabs/reeve/internal/config/schemas"
)

// Build returns the configured emitters.
func Build(cfg *schemas.Observability) []Emitter {
	if cfg == nil {
		return nil
	}
	var out []Emitter
	for _, a := range cfg.Annotations {
		events := parseEvents(a.Events)
		switch a.Type {
		case "grafana":
			out = append(out, &Grafana{BaseURL: a.URL, APIKey: a.APIKey, Events: events})
		case "datadog":
			out = append(out, &Datadog{BaseURL: a.URL, APIKey: a.APIKey, Events: events})
		case "dash0":
			ep := a.Endpoint
			if ep == "" {
				ep = a.URL
			}
			out = append(out, &Webhook{Name_: "dash0", Endpoint: ep, Headers: a.Headers, Events: events})
		case "webhook":
			out = append(out, &Webhook{Name_: "webhook", Endpoint: a.URL, Headers: a.Headers, Events: events})
		}
	}
	return out
}

func parseEvents(list []string) []EventType {
	out := make([]EventType, 0, len(list))
	for _, s := range list {
		out = append(out, EventType(s))
	}
	return out
}
