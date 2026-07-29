# Bump go.mongodb.org/mongo-driver to 1.17.7 — design

- **Date:** 2026-07-16
- **Status:** Implemented — branch `chore/bump-mongo-driver`
- **Area:** `go.mod` / `go.sum` only

## 1. Problem

Dependabot alert #19: CVE-2026-2303 (GHSA-cp6g-7hqx-qxhp, medium) — heap
out-of-bounds read in GSSAPI error handling of `go.mongodb.org/mongo-driver`
< 1.17.7. nodecore pins v1.17.4 as an **indirect** dependency pulled in by
`github.com/deckarep/golang-set/v2`.

## 2. Impact assessment

`go mod why go.mongodb.org/mongo-driver` → *main module does not need this
package*: it is never imported by any package in nodecore's build graph, so
the vulnerable code does not ship in the binary. The bump exists to clear the
alert and to be safe against future transitive imports.

## 3. Change

`go get go.mongodb.org/mongo-driver@v1.17.7 && go mod tidy` — go.sum delta
only, no code changes, `go build ./...` clean.

## 4. Testing

Regular CI (build/test/lint/e2e); no targeted tests possible or needed for an
unused transitive dependency.
