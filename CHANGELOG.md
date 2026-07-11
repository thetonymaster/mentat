# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0/).

## [Unreleased]

### Changed

- **Fixture/trace strictness (breaking).** Span `status` and `kind` spellings are
  now normalized through a canonical vocabulary at store-decode time. Unknown
  spellings that previously loaded silently now fail loudly with a decode error
  naming the span and the offending value
  (`filestore: span 3 ("checkout"): trace: unknown span status "FOO"`).
- OTLP wire spellings keep working: `STATUS_CODE_UNSET`/`STATUS_CODE_OK`/
  `STATUS_CODE_ERROR` and `SPAN_KIND_INTERNAL`/`SPAN_KIND_SERVER`/
  `SPAN_KIND_CLIENT`/`SPAN_KIND_PRODUCER`/`SPAN_KIND_CONSUMER` normalize to the
  canonical set; omitted `status` → `Unset`, omitted `kind` → unspecified.
- **Fixture `parentIndex` is now required and validated for a forest (breaking).**
  Every span must set `parentIndex` (`-1` = root); an omitted value used to decode
  to `0` and silently attach the span to span 0 — it now fails loudly
  (`filestore: span 2 ("payment"): parentIndex is required (use -1 for root)`).
  Parentage is also walked for reachability, so cyclic/rootless fixtures
  (e.g. `0 → 1 → 0`) are rejected instead of loading as a non-forest.

### Fixed

- Error assertions (`no span has status "ERROR"`, `MaxErrors`, CEL `errors`,
  `span.status=Error` selectors) now count spans that arrive with the live-Tempo
  wire spelling `STATUS_CODE_ERROR`, not only the in-repo fixture spelling — they
  were permanently green on live traces before. Closes audit finding A1
  (`docs/audits/2026-07-01-codebase-audit.md`) and the `002-verdict-integrity`
  spec (`specs/002-verdict-integrity/`).
