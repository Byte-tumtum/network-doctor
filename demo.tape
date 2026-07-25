# VHS tape for the README demo GIF (https://github.com/charmbracelet/vhs).
# Regenerate with:  go build -o netdoc . && vhs demo.tape
# Needs a working network connection; probes run live against github.com:443.
# The network-map segment sweeps the local /24 and shows real device names —
# record on a network you're happy to publish.

Output assets/demo.gif

Set FontSize 15
Set Width 1000
Set Height 650
Set Padding 12
Set TypingSpeed 40ms

Type "./netdoc github.com:443"
Sleep 500ms
Enter

# Let the probe chain complete and the verdict banner settle.
Sleep 5s

# Walk the checks so the detail pane changes.
Down@600ms 4
Sleep 1.5s
Down@600ms 3
Sleep 1.5s

# Full-screen help overlay.
Type "?"
Sleep 3s
Escape
Sleep 500ms

# Launch a slow traceroute; it streams hops into the job pane.
Type "t"
Sleep 2.5s

# Start mtr while the traceroute is still running — two concurrent jobs.
Type "m"
Sleep 2s

# Tab back to the streaming traceroute; the status line shows both jobs.
Tab
Sleep 2.5s

# Open the full output viewer on it, scroll, then filter the output.
Enter
Sleep 2s
Type "k"
Sleep 300ms
Type "k"
Sleep 300ms
Type "j"
Sleep 1s
Type "/"
Sleep 500ms
Type "ms"
Sleep 1s
Enter
Sleep 2.5s
Escape
Sleep 500ms

# mtr finished meanwhile — tab over to its report.
Tab
Sleep 3s

# Network map: the /24 sweep sits behind the same confirm gate as nmap;
# the map opens on launch and fills as hosts are found.
Type "v"
Sleep 2s
Type "y"
Sleep 12s
Down@600ms 2
Sleep 2s
Type "v"
Sleep 500ms

# Restart prompt: editable arguments with target history on the up arrow.
Type "r"
Sleep 1.5s
Up
Sleep 1.5s
Escape
Sleep 500ms

# Save a report of the chain plus the completed tool output.
Type "w"
Sleep 3s
