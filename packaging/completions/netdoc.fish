# fish completion for netdoc(1)
#
# Hand-maintained: netdoc uses the stdlib flag package, which has no completion
# generator. Keep this in sync with the flags in main.go.

# Targets are hostnames, URLs, and IP literals — none of them enumerable, so
# suppress the file completion fish would otherwise offer.
complete -c netdoc -f

# The stdlib flag package accepts both spellings of every flag, so each one is
# declared as a short option (-json) and a long option (--json).
complete -c netdoc -o toolbox -l toolbox -d 'Start in toolbox mode'
complete -c netdoc -o json -l json -d 'Run the checks headless and print a JSON report'
complete -c netdoc -o watch -l watch -d 'Continuously re-run checks'
complete -c netdoc -o version -l version -d 'Print version and exit'
complete -c netdoc -s h -o help -l help -d 'Print usage and exit'

complete -c netdoc -o iface -l iface -r -d 'Bind probes to an interface name or exact local IP' \
    -a '(command ls /sys/class/net 2>/dev/null)'
complete -c netdoc -o public-dns -l public-dns -r -d 'Second-opinion DNS resolver IP, empty to skip (default 8.8.8.8)'
complete -c netdoc -o timeout -l timeout -r -d 'Per-check probe timeout (default 4s)'
