# bash completion for netdoc(1)
#
# Hand-maintained: netdoc uses the stdlib flag package, which has no completion
# generator. Keep this in sync with the flags in main.go.

_netdoc() {
    local cur prev
    cur=${COMP_WORDS[COMP_CWORD]}
    prev=${COMP_WORDS[COMP_CWORD-1]}

    case $prev in
        -iface | --iface)
            # Interface names only. -iface also takes a local IP address, which
            # is not offered here: the useful ones are already on these links.
            COMPREPLY=($(compgen -W "$(command ls /sys/class/net 2>/dev/null)" -- "$cur"))
            return
            ;;
        -timeout | --timeout)
            # A Go duration string; nothing to enumerate.
            return
            ;;
    esac

    # Targets are hostnames, URLs, and IP literals — none of them enumerable, so
    # a non-flag word completes to nothing rather than to local filenames.
    if [[ $cur == -* ]]; then
        COMPREPLY=($(compgen -W "
            -toolbox --toolbox
            -json --json
            -watch --watch
            -iface --iface
            -timeout --timeout
            -version --version
            -h -help --help
        " -- "$cur"))
    fi
}

complete -F _netdoc netdoc
