# Contributing to NodeCore

## Helm Chart Schema Guidelines

1. Always update `values.schema.json` when modifying `values.yaml`.
2. CI will run `helm lint chart/nodecore` in PR workflow.
3. PRs changing chart files require review from CODEOWNERS (@drpcorg/infra).
