// Package proxy implements the reverse-proxy spy/mock engine: request
// matching and dispatch (Handler), upstream forwarding with rewrite/
// transform/fault injection (Engine), and host allow/deny policy (match.go).
// The transparent forward-proxy/MITM mode (on-the-fly cert signing from a
// Lyrebird CA) is not yet implemented — tracked separately as a follow-up
// milestone (tasks.md T054/T067).
package proxy
