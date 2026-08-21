# VHS tape for the README hero GIF (https://github.com/charmbracelet/vhs).
# One story, start to finish: a host that doesn't resolve, the chain stopping at
# the layer that broke, and the fix.
#
# Regenerate with:
#
#   CGO_ENABLED=0 go build -o netdoc .
#   CGO_ENABLED=0 go build -o netdoc-sim ./cmd/netdoc-sim
#   hero_home=$(mktemp -d)
#   HOME="$hero_home" vhs hero.tape
#   XDG_RUNTIME_DIR="$hero_home" ./netdoc-sim cleanup -all
#   rm -rf "$hero_home"
#
# CI records this tape too, on every change to the Go code or to the tape, in
# .github/workflows/demo.yml through charmbracelet/vhs-action. That run is what
# holds the tape to the program: it fails if a flag, a key binding or the
# startup path the tape depends on has moved. It does not compare its recording
# against the committed assets/hero.gif and does not commit one, because the
# screen carries live latencies and an animated spinner, so no two recordings
# are alike. To refresh the committed GIF, record locally with the line above,
# or download the hero-gif artifact from a run of that workflow.
#
# The tape starts the built-in dns-nxdomain scenario before recording. Every
# probe runs inside its rootless Linux namespaces against simulator-owned DNS,
# HTTP, QUIC and encrypted-DNS services. Nothing reaches the host network or
# the public internet.
#
# Record under a throwaway HOME so netdoc's target-history file and the kept
# simulator state are written to a temp dir instead of your own config.
#
# No visible keypresses follow the command: the TUI moves the cursor to the
# first failed row itself, so what you see is what a first-time user sees.

Output assets/hero.gif

Set FontSize 16
# Wide enough that the longest probe name ("Path MTU <host>:<port>") stays on
# one line in the checks panel; tall enough for all thirteen rows plus the
# verdict banner and the key bar, with no job pane to leave room for.
Set Width 1100
Set Height 640
Set Padding 12
Set TypingSpeed 45ms

# Put both freshly built binaries on PATH. The simulator first grades the same
# scenario once through its normal runner, then keeps the network alive. Enter
# the director's user namespace before the client network and mount namespaces,
# and trust only the simulator-generated roots used by the ordinary runner.
# All of this is hidden because the visible story begins with the user's command.
Hide
Type "export PATH=$PWD:$PATH XDG_RUNTIME_DIR=$HOME"
Enter
Type "netdoc-sim run dns-nxdomain -keep -netdoc $PWD/netdoc >$HOME/hero-sim.log 2>&1 &"
Enter
Type "hero_deadline=$((SECONDS + 30))"
Enter
Type `while ! hero_state=$(find $XDG_RUNTIME_DIR/netdoc-sim -name '*.json' -type f -print -quit 2>/dev/null) || [ -z "$hero_state" ]; do if (( SECONDS >= hero_deadline )); then cat $HOME/hero-sim.log; exit 1; fi; sleep .05; done`
Enter
Type "hero_id=${hero_state##*/}; hero_id=${hero_id%.json}"
Enter
Type `hero_director=$(awk '/"pid":/{gsub(/[^0-9]/,""); print; exit}' $hero_state)`
Enter
Type `hero_client=$(awk '/"name": "client"/{client=1} client && /"pid":/{gsub(/[^0-9]/,""); print; exit}' $hero_state)`
Enter
Type `hero_workspace=$(awk -F'"' '/"workspace":/{print $4; exit}' $hero_state)`
Enter
Type `netdoc() { nsenter -t $hero_director -U -n -m --preserve-credentials -- nsenter -t $hero_client -n -m -- env SSL_CERT_DIR=$hero_workspace/probe-roots $PWD/netdoc "$@"; }`
Enter
Type "clear"
Enter
Show

Type "netdoc printer.office.test"
Sleep 400ms
Enter

# The chain resolves in well under a second: link, egress and QUIC pass, DNS
# fails, and everything downstream of DNS is skipped rather than guessed at.
# The banner names the failure in a sentence and the fix under it; the cursor
# lands on the failed row on its own, so the details pane already holds the
# evidence. Hold there, because that is the whole story.
Sleep 10s

# Leave no kept simulator or workspace behind. Hide before quitting so the GIF
# ends on the diagnosis instead of flashing back to the shell prompt.
Hide
Type "q"
Sleep 500ms
Type "netdoc-sim cleanup $hero_id >/dev/null"
Enter
