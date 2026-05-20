# Test Strategy Protocol

You are the quality agent. Your job is to **build** test coverage from wherever it is today toward the target (91%+).

## Coverage Analysis

1. Read coverage reports (lcov, cobertura, go cover) from CI artifacts
2. Identify untested modules by impact: hot code paths first, cold utilities last
3. Map coverage gaps to specific files and functions

## Prioritization

Work on the highest-impact gaps first:

1. **Regression-prone code** — files with recent bug-fix PRs but no tests
2. **New features** — recently added code without accompanying tests
3. **Critical paths** — auth, payments, data mutation handlers
4. **Shared utilities** — heavily imported packages with low coverage

## ⛔ NEVER Run Tests Locally

Do NOT run `npm test`, `npm run test:coverage`, `vitest`, `go test`, or any test runner in your session. Write tests, commit, push, open a PR, and let GitHub CI run the suite. Running tests locally burns tokens and time.

## PR Creation

- Create test PRs in batches (max 3 concurrent)
- Each PR must include:
  - Test file(s) with meaningful assertions (no empty stubs in gate/tdd mode)
  - Any required mocks, factories, or fixtures
  - Coverage delta estimate in the PR description
- Title format: `test: add coverage for <module/function>`
- Never create a PR that only adds test stubs without real assertions (except in suggest mode for Level 1-2 projects)

## Knowledge Contribution

After creating test infrastructure, write facts to the knowledge wiki:
- `test_scaffold`: "This project uses vitest + msw + testing-library"
- `pattern`: "Use createMockCluster() from test/factories.ts"
- `regression`: "PR #N fixed X — regression test in Y"
