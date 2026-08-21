# VHS tape for the README's full-tour GIF (https://github.com/charmbracelet/vhs).
# It covers every feature, for a reader who is already interested. The README's hero clip
# is one diagnosis instead, and comes from hero.tape.
#
# Regenerate with:
#
#   go build -o netdoc . && HOME=$(mktemp -d) vhs demo.tape
#
# Local only, unlike hero.tape. The network-map segment below sweeps the
# recording machine's own /24 with nmap and the SSH form fills itself from the
# current user, neither of which a hosted CI runner has any business doing, so
# .github/workflows/demo.yml records the hero tape and leaves this one alone.
#
# Needs a working network connection; probes run live against github.com:443.
# The network-map segment sweeps the local /24 and shows real device names, so
# record on a network you're happy to publish.
#
# Record under a throwaway HOME so nothing personal reaches the GIF and the
# recording is reproducible. Two panes read from it:
#
#   $HOME/.ssh/id_ed25519{,.pub}      the SSH form lists key names
#   $HOME/.config/netdoc/history      the restart prompt's ↑ history
#
# Seed both before recording (an empty file passes for a key, because the form only
# stats the private half next to a .pub):
#
#   H=$(mktemp -d); mkdir -p $H/.ssh $H/.config/netdoc
#   touch $H/.ssh/id_ed25519 $H/.ssh/id_ed25519.pub
#   printf 'cloudflare.com\nexample.com:80\n1.1.1.1:53\n' > $H/.config/netdoc/history
#
# Without that, the form lists your real key names and the GIF needs blacking
# out frame-by-frame afterwards.

Output assets/demo.gif

Set FontSize 15
# 1300px is the narrowest width that fits the longest verdict sentence (the
# split-DNS one) without the summary line being cut off. Height must clear the
# check list, the toolbox chips and the help bar before the job pane gets any
# rows at all: at 600px the streaming tool output is silently shed and never
# appears on the main screen. 820px leaves it about eight lines.
Set Width 1300
Set Height 820
Set Padding 12
Set TypingSpeed 40ms

Type "./netdoc github.com:443"
Sleep 500ms
Enter

# Let the probe chain complete and the verdict banner settle.
Sleep 6s

# Walk the checks so the detail pane changes.
Down@600ms 4
Sleep 1.2s
Down@600ms 3
Sleep 1.2s

# Full-screen help overlay.
Type "?"
Sleep 3s
Escape
Sleep 500ms

# Launch a slow traceroute; it streams hops into the job pane.
Type "t"
Sleep 2.5s

# Start mtr while the traceroute is still running, so two jobs run at once.
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

# Network map: the /24 sweep sits behind the same confirm gate as nmap;
# the map opens on launch and fills as hosts are found.
Type "v"
Sleep 2s
Type "y"
Sleep 10s
Down@600ms 2
Sleep 2s
Type "v"
Sleep 500ms

# Tab over to the mtr report, which has had time to finish by now: report mode
# prints nothing at all until every cycle is done (~20s to a distant host), so
# this segment sits here rather than right after the launch.
Tab
Sleep 4s

# SSH login form: username, key picker, password. The form opens on the
# username, so tab first; nothing is submitted here, and esc backs out.
Type "S"
Sleep 2s
Tab
Sleep 800ms
Right@700ms 1
Sleep 1.5s
Tab
Sleep 500ms
Type "hunter2"
Sleep 1.5s
Escape
Sleep 500ms

# Restart prompt: editable arguments with target history on the up arrow.
# The prompt opens on the current target, which is also the newest history
# entry, so the first Up is a no-op and the second one is the visible step.
Type "r"
Sleep 1.5s
Up@700ms 2
Sleep 1.5s
Escape
Sleep 500ms

# Save a report of the chain plus the completed tool output.
Type "w"
Sleep 3s
Type "q"
Sleep 1s

# Toolbox mode: no chain, just the tools, with local state one keypress away.
Type "./netdoc --toolbox"
Sleep 500ms
Enter
Sleep 2s
Type "i"
Sleep 2.5s
Type "s"
Sleep 2.5s
Type "q"
Sleep 1s

# Probe selection plus a different second-opinion resolver: only the named
# checks and their prerequisites run.
Type "./netdoc --check dns,dns_public,target_tcp,tls --public-dns 9.9.9.9 github.com:443"
Sleep 500ms
Enter
Sleep 4s
Type "q"
Sleep 1s

# Headless JSON for scripts and CI; the tail carries the verdict. stderr is
# dropped because quic-go logs a UDP-buffer warning there on many Linux hosts,
# which is noise in the recording rather than anything about the report.
Type "./netdoc --json github.com:443 2>/dev/null | tail -18"
Sleep 500ms
Enter
Sleep 6s

# Watch mode: the same chain re-run every 5s, with a per-check history strip
# and failure count. Three passes is enough to show the strip filling in.
Type "./netdoc --watch github.com:443"
Sleep 500ms
Enter
Sleep 14s
Ctrl+C
Sleep 1s
