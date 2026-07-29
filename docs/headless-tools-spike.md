# Headless drill-down tools spike

Status: deferred pending a concrete automation request.

## Decision

Do not add headless tool execution yet. `--json` remains native-probes-only,
and no drill-down tool runs unless a user explicitly selects it in the TUI.
The [issue tracker](https://github.com/heymaikol/network-doctor/issues) had no
open requests for headless tools when this was reviewed on 2026-07-28.

The current split is deliberate:

- `main.runJSON` builds and runs only the native probe DAG, then emits the
  stable JSON report.
- `internal/ui.toolsFor` builds the platform-specific drill-down allowlist.
- `internal/ui.startTool` owns bounded subprocess execution and sanitized,
  capped output, but reports progress as Bubble Tea messages.
- `model.report` adds each selected TUI job's displayed command, duration,
  route-quality summary, and retained output tail to the human report.

There is no headless tool result type today. Reusing the TUI job directly
would couple the machine-readable path to Bubble Tea state and would still
leave selection, active-scan consent, and exit semantics undefined.

## Smallest viable future contract

If real demand appears, start with repeated allowlisted selections:

```text
netdoc --json --tool ping --tool dns example.com
```

No `--tool` keeps the current behavior exactly. Tool IDs must be stable names,
not shell commands or TUI hotkeys; the application continues to construct all
arguments. An explicit `all-safe` selection may be useful, but must omit the
target and LAN `nmap` scans.

Active scans must never be inferred from a failed probe or included in a
group selection. If automation needs them, require both the exact scan ID and
a separate `--allow-active-scan` flag. Headless mode must not prompt.

Selected tool results could be an optional top-level `tools` array:

```json
{
  "id": "ping",
  "command": ["ping", "-c", "4", "example.com"],
  "status": "DONE",
  "ms": 1032,
  "exit_code": 0,
  "summary": {},
  "output": ["64 bytes from 93.184.216.34: ..."],
  "evicted": 0,
  "dropped": 0
}
```

Keep the existing subprocess guarantees: no shell, no privilege escalation,
per-tool deadlines, process-group cancellation, sanitized output, bounded
line length, and bounded retained lines. Output may contain sensitive local
or remote data, so the existing review-before-sharing warning still applies.

Implementation should first separate tool definitions and process collection
from Bubble Tea delivery; it should not move tool behavior into
`internal/diagnostic`, whose verdicts intentionally depend only on native
probes. The TUI and headless adapters can then share the same allowlist,
timeouts, sanitizer, and result data.

## Questions for the first requester

The real automation use case should decide:

- whether selected-tool failure affects the process exit code or only a
  separate `tools_ok` field;
- whether tools run serially in selection order or concurrently;
- whether JSON needs every retained line or the same 15-line report tail;
- which summaries, beyond existing destination loss and latency, need stable
  structured fields.

Do not freeze flags or JSON fields until those answers come from an actual
consumer.
