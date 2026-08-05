// Package all compiles in the default channel set via blank imports. Commands
// import it once; a build that wants a subset imports the channel packages it
// needs instead (modularity contract: the factory itself never statically
// imports concrete channels).
package all

import (
	_ "github.com/FynxLabs/reeve/internal/notify/channels/github_issue"
	_ "github.com/FynxLabs/reeve/internal/notify/channels/otel"
	_ "github.com/FynxLabs/reeve/internal/notify/channels/pagerduty"
	_ "github.com/FynxLabs/reeve/internal/notify/channels/slack"
	_ "github.com/FynxLabs/reeve/internal/notify/channels/timeline"
	_ "github.com/FynxLabs/reeve/internal/notify/channels/webhook"
)
