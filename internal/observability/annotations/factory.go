package annotations

import (
	"fmt"

	"github.com/reeveops/reeve/internal/config/schemas"
)

// Build returns the configured emitters, or an error if the config names a
// type that does not exist. An unknown type used to be skipped in silence,
// so a typo in observability.yaml produced no annotations and no complaint;
// notify.Build has always rejected unknown channel types loudly.
func Build(cfg *schemas.Observability) ([]Emitter, error) {
	if cfg == nil {
		return nil, nil
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
		default:
			return nil, fmt.Errorf("unknown annotation type %q (want grafana, datadog, dash0, or webhook)", a.Type)
		}
	}
	return out, nil
}

func parseEvents(list []string) []EventType {
	out := make([]EventType, 0, len(list))
	for _, s := range list {
		out = append(out, EventType(s))
	}
	return out
}
