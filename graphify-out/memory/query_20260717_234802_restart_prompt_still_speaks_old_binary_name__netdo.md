---
type: "query"
date: "2026-07-17T23:48:02.132839+00:00"
question: "Restart prompt still speaks old binary name; netdoc full-command input fails in parseRunArgs."
contributor: "graphify"
outcome: "useful"
source_nodes: ["model", "parseRunArgs", "TestRestartPrompt"]
---

# Q: Restart prompt still speaks old binary name; netdoc full-command input fails in parseRunArgs.

## Answer

Expanded from original query via vocab: [restart, prompt, parse, args, binary, run, target, model, network, doctor]. The restart prompt in model.handleKey feeds editable input to parseRunArgs through handlePromptKey. Changing the displayed prefix to netdoc and accepting netdoc as an optional leading executable name fixes the mismatch; retaining network-doctor supports the module-derived go install binary. TestRestartPrompt is the focused regression check.

## Outcome

- Signal: useful

## Source Nodes

- model
- parseRunArgs
- TestRestartPrompt