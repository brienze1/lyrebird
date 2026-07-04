package mcp

import "github.com/brienze1/lyrebird/internal/domain"

// resolveSpace substitutes configuredDefault (cfg.DefaultSpace, threaded in
// at server construction) for an empty space argument, falling back to
// domain.DefaultPartitionID only if that's also empty — matching
// httpmw.Partition's behavior exactly, so REST and MCP never diverge on
// default-space semantics (constitution Principle II). MCP tool calls have
// no HTTP header to read a space from, unlike REST's X-Lyrebird-Space, so
// every tool's input carries its own optional Space field instead.
func resolveSpace(space, configuredDefault string) string {
	if space != "" {
		return space
	}
	if configuredDefault != "" {
		return configuredDefault
	}
	return domain.DefaultPartitionID
}
