<!--
Sync Impact Report
- Version change: (template) → 1.0.0
- Ratification: initial adoption 2026-07-03
- Principles defined:
  1. Generic-First — No Per-Service Code
  2. Agent-First Control Plane (MCP)
  3. Spy by Default, Disposable by Design
  4. Clean Architecture & Test-First (BDD)
  5. Secure & Frictionless Defaults
  6. Ship Continuously
- Added sections: Technology & Delivery Constraints; Development Workflow & Quality Gates; Governance
- Templates reviewed for alignment:
  - .specify/templates/plan-template.md ✅ (Constitution Check gate references these principles)
  - .specify/templates/spec-template.md ✅ (spec stays WHAT/WHY; tech deferred to plan — matches P4)
  - .specify/templates/tasks-template.md ✅ (test-first task ordering matches P4)
- Deferred TODOs: none
-->

# Lyrebird Constitution

Lyrebird is a generic, agent-driven HTTP(S) mock and proxy server, delivered as a Docker image and
controlled entirely through an MCP interface. This constitution defines the non-negotiable
principles that govern how it is designed, built, and evolved. All specs, plans, and tasks MUST
comply; deviations require an explicit, documented justification (see Governance).

## Core Principles

### I. Generic-First — No Per-Service Code

Lyrebird MUST mock any HTTP(S) endpoint through a single generic engine. It MUST NOT contain
per-service emulation code or per-service tooling (no "SNS handler", no "DynamoDB emulator", no
working queues/tables). Support for AWS/GCP/third-party SDKs is delivered as **knowledge** — an
examples/recipe library surfaced over MCP — never as service-specific branches in the codebase.

Rationale: cloud providers change constantly; per-service emulation is an unbounded maintenance
treadmill. A generic engine plus documented recipes covers "any HTTP" with a fixed, ownable surface.
A change request that would add a service-specific code path is a constitution violation and MUST be
redesigned as generic behavior + a recipe.

### II. Agent-First Control Plane (MCP)

The MCP interface is the primary, first-class control plane; the Admin REST API is a thin twin over
the same use-case layer and MUST NOT expose capabilities the MCP lacks. There is NO graphical UI.
Every MCP tool MUST carry model-oriented descriptions (a minimal valid example payload) and return
explanatory errors that state what failed and why. Business logic MUST live in the use-case layer,
never duplicated between MCP and REST adapters.

Rationale: the product exists to be driven by AI agents. If a capability is hard for a model to
discover or invoke correctly, it is incomplete.

### III. Spy by Default, Disposable by Design

Unmatched requests MUST be recorded and transparently proxied to the real upstream (spy mode); a
matching mock overrides that passthrough. Recorded traffic MUST capture the full request and full
upstream response. All persisted state is disposable: losing it on crash/restart MUST be acceptable
and MUST NOT corrupt behavior. A background garbage collector MUST bound stored data by a
configurable retention window. Lyrebird MUST NOT emulate stateful business data.

Rationale: a mock server is an ephemeral test fixture. Spy-by-default makes it useful the instant
it is deployed; disposability keeps operations trivial and safe.

### IV. Clean Architecture & Test-First (BDD)

Code MUST follow clean architecture: a dependency-free domain, a use-case layer, and adapters
(MCP, REST, proxy, store) that depend inward only. Behavior MUST be specified as BDD feature files
BEFORE implementation, and those tests MUST fail before they pass (red → green). The specification
document (spec.md) describes WHAT/WHY and MUST stay technology-agnostic; technology choices live in
the plan.

Rationale: testability and inward-only dependencies are what let the project be extended by both
humans and agents without regressions.

### V. Secure & Frictionless Defaults

Security features follow one pattern: **open/frictionless by default, hardened by setting env
vars.** The data plane (mock/proxy listeners) MUST NEVER require authentication. Control-plane auth
is OFF unless auth env vars are set; when set, control-plane calls require short-lived tokens
(default 1h). Encryption at rest for sensitive payloads is ON by default using a startup-generated
key; a stable key MAY be supplied via env for restart-durable volumes. No secret material may be
written to logs or committed to the repo.

Rationale: frictionless local use drives adoption; a single env var must be enough to make a shared
deployment safe, with no code or image difference.

### VI. Ship Continuously

Lyrebird MUST be deliverable as a small, static, multi-arch Docker image. CI/CD MUST exist from the
first commit: pull requests are gated by lint + tests + a build; merges to the main branch publish
the public image automatically. Each roadmap milestone MUST be an independently useful, shippable
vertical slice.

Rationale: continuous, automated delivery of a public image is a product requirement, not an
afterthought — it must never be bolted on late.

## Technology & Delivery Constraints

- Language: Go. The core MUST compile as a static, `CGO_ENABLED=0` binary (pure-Go dependencies,
  including the SQLite driver) to keep the image tiny and portable.
- Persistence: embedded SQLite for the disposable traffic log and ephemeral mocks. Seeded mocks load
  from mounted config files at boot and are protected from reset/GC.
- Scripting: advanced rule logic uses an embedded, sandboxed JavaScript engine (no CGO, no
  filesystem/network access beyond an explicitly injected helper API), guarded by execution timeout
  and memory limits.
- Distribution: public image on GitHub Container Registry; multi-arch (linux/amd64 + linux/arm64).
- Interfaces: MCP over Streamable HTTP (remote) and stdio (local); Admin REST as the thin twin.

## Development Workflow & Quality Gates

- Spec-Driven: features flow through specify → clarify → plan → tasks → analyze before implementation.
- BDD-First: feature files are authored and failing before implementation begins (Principle IV).
- PR gate (CI): `go vet`, linter, full test suite, and a Docker build MUST pass before merge.
- Constitution Check: every plan MUST include a Constitution Check confirming compliance with all
  six principles; any violation MUST be recorded in the plan's Complexity/Deviation tracking with a
  concrete justification or the design MUST change.
- No UI work, no per-service emulation code, and no data-plane auth may be introduced under any
  milestone.

## Governance

This constitution supersedes ad-hoc practice. Amendments MUST be proposed via pull request that
updates this file, states the rationale, and bumps the version per semantic versioning:

- MAJOR: removal or backward-incompatible redefinition of a principle or governance rule.
- MINOR: a new principle/section or materially expanded guidance.
- PATCH: clarifications and wording that do not change obligations.

Compliance is reviewed at each Spec Kit gate (specify/plan/tasks/analyze) and at PR review. Any
merged deviation without a recorded justification is a defect to be corrected. When guidance here
conflicts with convenience, this document wins.

**Version**: 1.0.0 | **Ratified**: 2026-07-03 | **Last Amended**: 2026-07-03
