# Real network field corpus

This directory stores sanitized Network Doctor evidence from real networks,
with ground truth established independently of Network Doctor. It is not a
simulator fixture set, a snapshot golden directory, or an offline replay
engine. The corpus starts empty rather than presenting synthetic evidence as a
field observation.

## Case format

Each case is a directory named for its stable ID:

```text
testdata/field/
  README.md
  2026-08-31-campus-split-dns/
    case.json
    snapshot.ndoc
```

Use a lowercase, hyphen-separated ID of at most 80 characters. A date plus a
short description is recommended. The directory and `case.json` ID must match.
The `.ndoc` must be the reviewed output of `netdoc --support`, not `--save`, and
must remain named `snapshot.ndoc`.

`case.json` uses schema `netdoc.field-case.v1`:

```json
{
  "schema": "netdoc.field-case.v1",
  "id": "2026-08-31-campus-split-dns",
  "environment": {
    "categories": ["campus", "split_dns"],
    "platform": "linux",
    "platform_details": "Fedora, amd64",
    "details": "Connected to the campus VPN"
  },
  "network_doctor": {
    "version": "1.2.3",
    "assessment": "mostly_correct"
  },
  "ground_truth": {
    "statement": "The internal name intentionally resolved only through campus DNS.",
    "verification": [
      {
        "method": "configuration_review",
        "details": "The network owner confirmed the split-DNS rule and resolver scope."
      }
    ]
  },
  "provenance": {
    "origin": "real_network",
    "summary": "Captured during a verified field report and reviewed by a maintainer."
  },
  "snapshot": "snapshot.ndoc",
  "notes": "Network Doctor detected disagreement but could not know it was intentional."
}
```

Environment categories are `vpn`, `corporate`, `campus`, `captive_portal`,
`public_wifi`, `ipv6_first`, `ipv6_only`, `dns64_nat64`, `split_dns`, `proxy`,
`custom_dns`, and `other`. Add `environment.details` when using `other`.
Platforms use Go names: `linux`, `darwin` for macOS, and `windows`.

Assessments are `correct`, `mostly_correct`, `incorrect`, and `uncertain`.
Verification methods are `controlled_change`, `independent_tool`,
`configuration_review`, `packet_capture`, `successful_remediation`,
`provider_confirmation`, and `other`. Verification details must say what was
observed or checked. Ground truth must not copy `snapshot.diagnosis`; that
object remains Network Doctor's original conclusion.

## Adding a verified case

1. Confirm the report came from a real network and independently establish the
   ground truth. Simulator output is never a field case.
2. Ask the reporter for a locally created `netdoc --support snapshot.ndoc ...`
   artifact. Record the producing version and broad, safe environment details.
3. Review both files manually. Remove private hostnames, addresses, SSIDs,
   usernames, paths, credentials, issue text, and confidential network details.
   Support redaction reduces risk but cannot prove an arbitrary string is safe.
4. Write `case.json`, keeping ground truth concise and naming how it was
   verified. Assess the diagnosis already stored in the snapshot.
5. Run `go test ./internal/fieldcase` and include both files in one focused
   review.

Validation discovers case directories, rejects unknown metadata fields and
unexpected files, requires real-network provenance, checks controlled
vocabularies and metadata consistency, decodes the current `.ndoc` schema,
requires the `support-v1` redaction marker, reapplies support redaction, and
requires canonical snapshot bytes. These checks catch structural and known
redaction mistakes. Human privacy review remains mandatory.

Future offline re-diagnosis may read this evidence and compare new conclusions
with the separate ground truth. Reconstructing probe results or rerunning the
diagnosis engine is intentionally outside this corpus layer.
