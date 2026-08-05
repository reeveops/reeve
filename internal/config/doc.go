// Package config loads and validates .reeve/*.yaml. Each file declares
// version: 1 + config_type: <type>; schemas live in internal/config/schemas.
// Strict unmarshal - unknown keys are errors.
//
// config_type values (v1): shared, engine, auth, notifications,
// observability, drift, and user (for ~/.config/reeve/*).
package config
