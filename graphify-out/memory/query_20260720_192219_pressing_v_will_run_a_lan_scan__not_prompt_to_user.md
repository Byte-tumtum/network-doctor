---
type: "query"
date: "2026-07-20T19:22:19.294371+00:00"
question: "Pressing v will run a LAN Scan, not prompt to user to run a LAN Scan. Also update the text to say that pressing v runs a network map and then it says lan scan."
contributor: "graphify"
outcome: "useful"
source_nodes: [".handleKey()", "lanDiscoveryTool()", ".helpView()", ".networkMapView()"]
---

# Q: Pressing v will run a LAN Scan, not prompt to user to run a LAN Scan. Also update the text to say that pressing v runs a network map and then it says lan scan.

## Answer

Expanded from original query via graph vocab: [lan, discovery, network, map, prompt, confirm, handle, key, tool, view]. The v branch in .handleKey() now launches or defers lanDiscoveryTool() directly instead of assigning confirmTool. lanDiscoveryTool() is named LAN scan and no longer requests confirmation. helpView() labels v as network map, and networkMapView() displays Network map — LAN scan. The nmap port-scan confirmation remains unchanged. Validation passed: go test ./..., go vet ./..., go build ./....

## Outcome

- Signal: useful

## Source Nodes

- .handleKey()
- lanDiscoveryTool()
- .helpView()
- .networkMapView()