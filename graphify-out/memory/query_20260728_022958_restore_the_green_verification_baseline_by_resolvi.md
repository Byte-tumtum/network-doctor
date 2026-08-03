---
type: "query"
date: "2026-07-28T02:29:58.790703+00:00"
question: "Restore the green verification baseline by resolving one unchecked cleanup and four intentional Unicode test literals so lint runs through vulnerability and release checks."
contributor: "graphify"
outcome: "useful"
source_nodes: ["model", "Clean()", "TestSanitize"]
---

# Q: Restore the green verification baseline by resolving one unchecked cleanup and four intentional Unicode test literals so lint runs through vulnerability and release checks.

## Answer

Expanded from original query via graph vocab: [model, sanitize, test, lint, release, clean]. Updated saveHistory cleanup to explicitly discard the best-effort os.Remove error and escaped all intentional Unicode format characters in sanitize_test.go. golangci-lint, govulncheck, GoReleaser check, vet, build, tests, race tests, and sanitizer fuzzing pass.

## Outcome

- Signal: useful

## Source Nodes

- model
- Clean()
- TestSanitize