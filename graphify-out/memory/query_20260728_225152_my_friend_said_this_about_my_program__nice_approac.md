---
type: "query"
date: "2026-07-28T22:51:52.323660+00:00"
question: "My friend said this about my program: Nice approach. One thing I would find especially useful is making the stages explicit as DNS → route → TCP → TLS → HTTP and treating ICMP as advisory, since many healthy hosts drop ping. A --json output with timings, resolved IPs, exit codes and the first failing stage would also make it handy for CI and bug reports. Do you already distinguish service unavailable from network path unavailable in that way?"
contributor: "graphify"
outcome: "useful"
source_nodes: ["BuildProbes()", "Diagnose()", "JSON output", ".httpProbe()"]
---

# Q: My friend said this about my program: Nice approach. One thing I would find especially useful is making the stages explicit as DNS → route → TCP → TLS → HTTP and treating ICMP as advisory, since many healthy hosts drop ping. A --json output with timings, resolved IPs, exit codes and the first failing stage would also make it handy for CI and bug reports. Do you already distinguish service unavailable from network path unavailable in that way?

## Answer

Expanded from original query via graph vocab: [dns, route, tcp, tls, http, json, duration, resolved, exit, stage, failure, diagnose]. Yes, mostly. The targeted probe DAG explicitly walks Interface → DNS → target TCP → TLS → HTTPS, and Diagnose reports the first failed protocol rung while using independent direct/proxy egress checks to distinguish a target or service-side failure from a broader local path failure. ICMP is not part of the automated probe DAG at all; ping is an optional toolbox command, so it cannot block or fail diagnosis. JSON already contains ordered check IDs/statuses, resolved addresses, selected/source IPs, interfaces, per-address TCP-attempt milliseconds/errors, summary, and ok; the process returns 0/1/2. It does not currently expose a route probe, a top-level exit_code field, an explicit first_failing_stage field, or an overall duration for each check. Also, any HTTP response including 3xx/4xx/5xx counts as reachable, so an HTTP 503 is not classified as an unhealthy service.

## Outcome

- Signal: useful

## Source Nodes

- BuildProbes()
- Diagnose()
- JSON output
- .httpProbe()