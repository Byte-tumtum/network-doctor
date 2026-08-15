---
layout: default
title: Network Doctor documentation
description: >-
  Documentation for Network Doctor, the cross-platform TUI that pinpoints
  whether a broken connection is your network, the path, or the service —
  install, read a diagnosis, and troubleshoot DNS, TCP, TLS, HTTP, proxy, and
  path-MTU failures.
permalink: /
---

<div class="hero" markdown="1">

# Network Doctor documentation

**Find exactly where your connection breaks.** Network Doctor is a
cross-platform network troubleshooting TUI that turns interface, DNS, TCP, TLS,
HTTP, proxy, and path-MTU checks into one plain-English diagnosis.

Instead of handing you a wall of `ping`, `dig`, and `curl` output, it answers
the useful question: **is the problem on my network, along the path, or at the
service?**

</div>

![Network Doctor diagnosing a host that will not resolve: the DNS row fails, every check that depended on it is skipped, and the verdict names the missing DNS record as the fix]({{ '/assets/hero.gif' | relative_url }})

## Start here

<div class="cards" markdown="1">

- ### [I have a network problem]({{ '/wiki/Getting-Started/' | relative_url }})

  [Getting Started]({{ '/wiki/Getting-Started/' | relative_url }}) installs it
  and explains the screen.
  [Understanding Your Diagnosis]({{ '/wiki/Understanding-Your-Diagnosis/' | relative_url }})
  turns a verdict into a next action.
  [Troubleshooting and FAQ]({{ '/wiki/Troubleshooting-and-FAQ/' | relative_url }})
  covers the rows that behave surprisingly.

- ### [I want to know how it decides]({{ '/wiki/How-Network-Doctor-Works/' | relative_url }})

  [How Network Doctor Works]({{ '/wiki/How-Network-Doctor-Works/' | relative_url }})
  explains why the probe branches are independent and why no verdict depends on
  ICMP. The exact probe table, flags, JSON schema, and exit codes are in the
  [reference]({{ '/docs/reference/' | relative_url }}).

- ### [I want to play Challenge Mode]({{ '/wiki/Challenge-Mode/' | relative_url }})

  [Challenge Mode]({{ '/wiki/Challenge-Mode/' | relative_url }}) hides a real
  fault in a throwaway virtual network and scores you against Network Doctor on
  the same one. [Simulator Overview]({{ '/wiki/Simulator-Overview/' | relative_url }})
  explains what `netdoc-sim` builds.

- ### [I want to contribute]({{ '/wiki/Development-and-Contributing/' | relative_url }})

  [Architecture]({{ '/wiki/Architecture/' | relative_url }}) covers the package
  boundaries and the probes → evidence → diagnosis flow.
  [Development and Contributing]({{ '/wiki/Development-and-Contributing/' | relative_url }})
  covers the clone, the build, and the validation gate.

</div>

## Common symptoms

- [DNS resolves but nothing loads: the path is unavailable]({{ '/wiki/Understanding-Your-Diagnosis/#network--the-path-is-unavailable' | relative_url }})
- [The path works but the far end does not answer]({{ '/wiki/Understanding-Your-Diagnosis/#service--the-path-works-the-far-end-does-not' | relative_url }})
- [A name did not resolve at all]({{ '/wiki/Understanding-Your-Diagnosis/#dns--the-name-did-not-resolve' | relative_url }})
- [Telling "my network" and "their service" apart, worked through]({{ '/wiki/Understanding-Your-Diagnosis/#worked-example-telling-the-two-important-cases-apart' | relative_url }})
- [The Path MTU row warned — what to actually do]({{ '/wiki/Troubleshooting-and-FAQ/#the-path-mtu-row-warned-what-do-i-actually-do' | relative_url }})
- [How path MTU is measured without root]({{ '/wiki/How-Network-Doctor-Works/#path-mtu-without-root' | relative_url }})
- [`ping` works but Network Doctor says the network is broken, or the reverse]({{ '/wiki/Troubleshooting-and-FAQ/#why-does-ping-work-but-netdoc-says-the-network-is-broken-or-vice-versa' | relative_url }})
- [A row says WARN — is that a failure?]({{ '/wiki/Troubleshooting-and-FAQ/#a-row-says-warn-is-that-a-failure' | relative_url }})
- [A row says N/A instead of failing]({{ '/wiki/Troubleshooting-and-FAQ/#why-is-a-row-na-instead-of-failing' | relative_url }})
- [The QUIC row fails but everything else works]({{ '/wiki/Troubleshooting-and-FAQ/#the-quic-row-fails-but-everything-else-works' | relative_url }})
- [Encrypted DNS fails but `dig` works fine]({{ '/wiki/Troubleshooting-and-FAQ/#encrypted-dns-fails-but-dig-works-fine' | relative_url }})
- [TLS failure causes, from expired certificates to hostname mismatch]({{ '/docs/reference/#how-it-diagnoses' | relative_url }})
- [A port that refuses versus one that is filtered]({{ '/wiki/How-Network-Doctor-Works/#why-each-branch-is-separate' | relative_url }})

## Where the documentation lives

This site publishes both halves of Network Doctor's documentation. The
explanatory half is written in the
[GitHub wiki](https://github.com/heymaikol/network-doctor/wiki) and the exact
reference half lives in
[`docs/`](https://github.com/heymaikol/network-doctor/tree/main/docs) beside the
code, so each page changes in the same place it always did — see
[Documentation Map]({{ '/wiki/Documentation-Map/' | relative_url }}) for what is
authoritative where.
