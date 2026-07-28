// Package all compiles in the default engine set via blank imports. Commands
// import it once; a build that wants a subset imports the engine packages it
// needs instead (modularity contract: the factory itself never statically
// imports concrete engines).
package all

import (
	_ "github.com/FynxLabs/reeve/internal/iac/pulumi"
	_ "github.com/FynxLabs/reeve/internal/iac/terraform"
	_ "github.com/FynxLabs/reeve/internal/iac/tofu"
)
