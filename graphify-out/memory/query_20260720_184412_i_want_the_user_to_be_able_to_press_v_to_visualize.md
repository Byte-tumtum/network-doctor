---
type: "query"
date: "2026-07-20T18:44:12.786255+00:00"
question: "I want the user to be able to press v to visualize the network"
contributor: "graphify"
outcome: "useful"
source_nodes: ["model", ".handleKey()", ".View()", "Probe"]
---

# Q: I want the user to be able to press v to visualize the network

## Answer

Expanded from original query via graph vocab: [network, view, render, key, press, model, update, probe, result, route, shortcut]. The main-screen key path is model.handleKey; View composes bodyView and helpView; Probe.Deps already supplies the dependency graph, so v can toggle a live ASCII map without a new subsystem.

## Outcome

- Signal: useful

## Source Nodes

- model
- .handleKey()
- .View()
- Probe