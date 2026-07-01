# Persisted State Compatibility Fixtures

KatlOS persisted state records are compatibility surfaces. Any accepted
`recordType` and `recordVersion` pair must have a static fixture under
`internal/installer/testdata/persisted/v1/`.

Fixture tests must decode each accepted envelope and pass it through the
strongest reader available for that record. Readers should validate payload
shape, record identity, path-derived identity, replay state, and recorded
digests when the record format carries those invariants.

Schema changes must update fixtures before landing:

- Adding a new persisted `recordType` or accepted version requires a new
  fixture and a reader assertion.
- Changing a payload field name, required field, enum value, digest input, or
  replay behavior requires updating the affected fixture and preserving tests
  for older accepted versions until the project explicitly drops that version.
- Negative fixtures must continue to cover unsupported newer versions, missing
  payloads, strict unknown payload fields, path/type mismatches, record ID
  mismatches, digest mismatches, invalid enum values, and malformed timestamps.

The fixtures are not generated during tests. Keeping them static makes
compatibility drift visible in review instead of silently following the current
writers.
