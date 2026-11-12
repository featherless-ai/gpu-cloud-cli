// Package featherlesscloud embeds package.json, the CLI's single source for its
// name and version: npm publishes the same version the binary reports.
package featherlesscloud

import _ "embed"

//go:embed package.json
var PackageJSON []byte
