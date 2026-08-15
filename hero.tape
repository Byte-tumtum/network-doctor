# VHS tape for the README hero GIF (https://github.com/charmbracelet/vhs).
# One story, start to finish: a host that doesn't resolve, the chain stopping at
# the layer that broke, and the fix.
#
# Regenerate with:
#
#   go build -o netdoc . && HOME=$(mktemp -d) vhs hero.tape
#
# Needs a working network connection: the interface, egress, QUIC and public-DNS
# rows are live probes. The failing row is the reproducible part —
# api.acme.invalid sits under the reserved `.invalid` TLD (RFC 6761), so
# every resolver everywhere answers NXDOMAIN and the recording lands the same
# way on any contributor's machine.
#
# Record under a throwaway HOME so netdoc's target-history file is written to a
# temp dir instead of your own config.
#
# No keypresses after the command: the TUI moves the cursor to the first failed
# row itself, so what you see is what a first-time user sees.

Output assets/hero.gif

Set FontSize 16
# Wide enough that the longest probe name ("Path MTU <host>:<port>") stays on
# one line in the checks panel; tall enough for all thirteen rows plus the
# verdict banner and the key bar, with no job pane to leave room for.
Set Width 1100
Set Height 640
Set Padding 12
Set TypingSpeed 45ms

# Put the freshly built binary on PATH and start from a clean screen, so the
# visible command is the one a reader would type after installing.
Hide
Type "export PATH=$PWD:$PATH"
Enter
Type "clear"
Enter
Show

Type "netdoc api.acme.invalid"
Sleep 400ms
Enter

# The chain resolves in well under a second: link, egress and QUIC pass, DNS
# fails, and everything downstream of DNS is skipped rather than guessed at.
# The banner names the failure in a sentence and the fix under it; the cursor
# lands on the failed row on its own, so the details pane already holds the
# evidence. Hold there — that is the whole story.
Sleep 10s
