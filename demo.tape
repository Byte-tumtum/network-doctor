# VHS tape for the README demo GIF (https://github.com/charmbracelet/vhs).
# Regenerate with:  go build -o netdoc . && vhs demo.tape
# Needs a working network connection; probes run live against github.com:443.
# The network-map segment sweeps the local /24 and shows real device names —
# record on a network you're happy to publish.
#
# The SSH form lists the private keys in ~/.ssh by name, so the published GIF
# has that value blacked out afterwards. Find the frame range where a real key
# (not "none") is on screen, then redact it — the range moves whenever the
# timings above change, so re-derive it rather than reusing these numbers:
#
#   ffmpeg -i raw.gif -filter_complex \
#     "[0:v]drawbox=x=152:y=438:w=155:h=20:color=black:t=fill:enable='between(n,1389,1515)',\
#      split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=bayer" \
#     -loop 0 assets/demo.gif

Output assets/demo.gif

Set FontSize 15
# 1300px is the narrowest width that fits the longest verdict sentence (the
# split-DNS one) without the summary line being cut off; 600px is the tallest
# pane stack (the SSH form).
Set Width 1300
Set Height 600
Set Padding 12
Set TypingSpeed 40ms

Type "./netdoc github.com:443"
Sleep 500ms
Enter

# Let the probe chain complete and the verdict banner settle.
Sleep 6s

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

# Open the full output viewer on it, then filter the output.
Enter
Sleep 2s
Type "/"
Sleep 500ms
# A carrier's hops, so the filter visibly drops rows instead of matching them all.
Type "twelve99"
Sleep 1s
Enter
Sleep 2.5s
# Two escapes: the first drops the committed filter, the second leaves the viewer.
Escape
Sleep 800ms
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

# SSH login form: username, key picker, password. The form opens on the
# username, so tab first; nothing is submitted here — esc backs out. This is
# the segment the post-processing blacks out.
Type "S"
Sleep 2s
Tab
Sleep 800ms
Right@700ms 2
Sleep 1.5s
Tab
Sleep 500ms
Type "hunter2"
Sleep 1.5s
Escape
Sleep 500ms

# Restart prompt: editable arguments with target history on the up arrow.
# The prompt opens on the current target, which is also the newest history
# entry — so the first Up is a no-op and the second one is the visible step.
Type "r"
Sleep 1.5s
Up@700ms 2
Sleep 1.5s
Escape
Sleep 500ms

# Save a report of the chain plus the completed tool output.
Type "w"
Sleep 3s

# Watch mode: the same chain re-run every 5s, with a per-check history strip
# and failure count. Three passes is enough to show the strip filling in.
Type "q"
Sleep 1s
Type "./netdoc --watch github.com:443"
Sleep 500ms
Enter
Sleep 16s
Ctrl+C
Sleep 1s
