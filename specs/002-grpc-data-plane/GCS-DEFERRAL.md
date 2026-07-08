# Governance note: GCS object store is DEFERRED (needs a constitution decision)

**Status**: Out of scope for feature `002-grpc-data-plane`. Requires an explicit governance
decision before any implementation.

## Context

The original brief for serving the creator-ads non-HTTP fakes had three parts. Two are shipped
generically by this feature (KMS Decrypt echo, Pub/Sub GetTopic/Publish — as gRPC recipes, zero
per-service code). The third, a **fake-gcs replacement**, is intentionally **not** built here.

## Why it is not just "another recipe"

A GCS emulator must **store uploaded bytes and serve them back**: a signed-URL `PUT` to a
dynamically-generated key, followed later by a `HEAD`/`GET` on that same key that must return the
stored object. That is real, persistent, stateful object storage — not a declarative
match→respond mock.

This conflicts directly with the constitution:

- **Principle I (Generic-First — No Per-Service Code)**: "MUST NOT contain per-service emulation
  code … no working queues/tables." A working object store is exactly this.
- **Principle III (Spy by Default, Disposable by Design)**: "Lyrebird MUST NOT emulate stateful
  business data." A store that must return what was PUT to it is stateful business data.

## Required next step (not an implementation TODO)

Building a GCS object store requires **amending the constitution first** (a MINOR or MAJOR version
bump per the Governance section), with a recorded rationale and a bounded design (e.g. an
explicitly disposable, GC'd, per-space blob store that does not claim to be "generic"). Until that
amendment exists, the creator-ads stack keeps its existing external GCS emulator; nothing in this
feature depends on GCS.

This is a decision for the team/maintainers, deliberately surfaced rather than silently coded.
