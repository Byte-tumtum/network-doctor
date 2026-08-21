# VHS tape for the README hero GIF (https://github.com/charmbracelet/vhs).
# One ordinary Network Doctor run: the machine can reach the internet, but a
# printer name does not exist, so the chain stops at DNS and explains the fix.
#
# Record on a clean Linux machine whose interface name and optional Wi-Fi name
# are safe to publish. The machine needs ordinary internet access, Go, VHS
# v0.11.0, plus ping, dig, curl, traceroute, mtr, ss, ip, nmap, and ssh so every
# Dig deeper action is available. VHS also needs its normal ttyd, browser,
# ffmpeg, and font setup.
#
# Regenerate with:
#
#   CGO_ENABLED=0 go build -o netdoc .
#   hero_config=$(mktemp -d)
#   XDG_CONFIG_HOME="$hero_config" vhs hero.tape
#   rm -rf "$hero_config"
#
# The temporary config directory keeps netdoc's target history out of the
# recording account without replacing HOME, which VHS and its renderer may use.
# The tape writes the tracked result to assets/hero.gif.
#
# The interface, egress, QUIC, public-DNS, and encrypted-DNS rows are live
# probes. printer.invalid uses the reserved .invalid namespace, so the target
# itself cannot unexpectedly acquire a DNS record.

Output assets/hero.gif

Set FontSize 16
# Wide enough that the longest probe name ("Path MTU <host>:<port>") stays on
# one line in the checks panel; tall enough for all thirteen rows plus the
# verdict banner and the key bar, with no job pane to leave room for.
Set Width 1100
Set Height 640
Set Padding 12
Set TypingSpeed 70ms

# Put the freshly built binary on PATH and start from a clean screen. This is
# the only hidden setup; netdoc remains the binary that the visible command runs.
Hide
Type "export PATH=$PWD:$PATH"
Enter
Type "clear"
Enter
Show

# Let the empty terminal breathe so the loop starts cleanly, then type exactly
# what someone with Network Doctor installed would type.
Sleep 800ms
Type "netdoc printer.invalid"
Sleep 400ms
Enter

# The live probes settle at their real speed. Hold only the completed result so
# the diagnosis, Fix, and Next lines can be read before the tape loops.
Sleep 4s
