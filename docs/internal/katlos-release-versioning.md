# KatlOS Release Versioning

Status: accepted for the first KatlOS release line.

KatlOS uses calendar versions in this SemVer-compatible form:

```text
YYYY.M.PATCH
YYYY.M.PATCH-dev.N
YYYY.M.PATCH-rc.N
```

`YYYY.M` identifies the calendar release line. The month is not zero-padded.
`PATCH` starts at `0` and increments when more than one stable KatlOS release is
cut in the same month. A release keeps its original version after the month
changes; the version records its release line, not the current date.

`dev.N` identifies development publications and `rc.N` identifies release
candidates. Both sequences start at `0` and increment for materially different
published artifacts. Development builds precede release candidates, which
precede the stable release:

```text
2026.7.0-dev.0
2026.7.0-dev.1
2026.7.0-rc.0
2026.7.0-rc.1
2026.7.0
```

The v0.1 project milestone is therefore a scope milestone, not the literal
KatlOS product version. Its first development publication is
`2026.7.0-dev.0`, and its first release candidate is `2026.7.0-rc.0`.

Git tags use a leading `v`, for example `v2026.7.0-rc.0`. Release branches use
the exact product version after `release/`, for example
`release/2026.7.0-rc.0`. The release tooling accepts an optional `v` in either
place, removes it from embedded artifact metadata, and rejects zero-padded
months, leading-zero counters, other prerelease labels, and non-calendar
versions. Manual workflow runs use the product version without the `v`.

The calendar version identifies a KatlOS release as a whole. Kubernetes payload
bundles and node-extension bundles retain their independent payload versions
and compatibility metadata; they do not inherit the KatlOS calendar version.
