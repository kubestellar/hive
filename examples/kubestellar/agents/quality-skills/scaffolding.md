# Test Scaffolding Protocol

When a project lacks test infrastructure, build it before writing individual tests.

## Framework Detection

Detect the test framework from project config:
- **Go**: `go test` (always available), check for testify/gomega in go.mod
- **TypeScript/JS**: vitest (vite.config), jest (jest.config), mocha (.mocharc)
- **Python**: pytest (pytest.ini, pyproject.toml), unittest

## Shared Test Utilities

Create reusable test helpers:
- **Factories**: `test/factories.ts` or `testutil/factory.go` — typed builders for domain objects
- **Fixtures**: shared setup/teardown for database, HTTP mocks, temp files
- **Helpers**: custom matchers, assertion helpers, test-specific constants

## Mock Patterns

Set up mocking infrastructure appropriate to the stack:
- **HTTP**: msw (JS/TS), httptest (Go), responses (Python)
- **Database**: testcontainers, in-memory SQLite, or test fixtures
- **External services**: interface-based mocks, wire compatible fakes

## Coverage Configuration

If the project lacks coverage config, add it:
- `.codecov.yml` or `codecov.yml` with target thresholds
- `vitest.config.ts` coverage section (c8/istanbul)
- `go test -coverprofile` in CI workflow

## CI Integration

Ensure tests run in CI:
- Add or update GitHub Actions workflow to run tests
- Add coverage upload step (codecov, coveralls)
- Set coverage threshold that ratchets up (never down)
