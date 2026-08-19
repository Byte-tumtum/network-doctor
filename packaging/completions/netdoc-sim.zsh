#compdef netdoc-sim

# zsh completion for netdoc-sim(1). Installed as _netdoc-sim on zsh's fpath.
#
# Hand-maintained: netdoc-sim uses the stdlib flag package, which has no
# completion generator. Keep this in sync with the flag sets in cmd/netdoc-sim.

# The scenario library is whatever this build knows, so ask the binary rather
# than keeping a copy of the list here.
_netdoc_sim_scenarios() {
  local -a names
  names=(${(f)"$(netdoc-sim scenarios 2>/dev/null)"})
  (( $#names )) && _describe -t scenarios 'scenario' names
}

# No -s anywhere below: it would let single-letter options stack, and the
# single-dash long spellings (-json) would be read as stacked letters.
local state
local -a commands
commands=(
  'run:build the network, run the tests, print the report'
  'campaign:run a seeded scenario campaign sequentially'
  'hunt:generate deterministic faults and rank likely bugs'
  'triage:hunt the fixed baselines, reproduce findings, file issues'
  'challenge:diagnose a hidden fault yourself, then let netdoc try'
  'validate:parse and check a scenario without building anything'
  'scenarios:list the built-in scenarios'
  'starters:list the curated starter packs, or one pack'"'"'s challenges'
  'authored:list the hand-written challenges and their ids'
  'capabilities:report whether this host can simulate'
  'list:list simulations left running by run -keep'
  'inspect:show a kept simulation'"'"'s nodes and how to enter them'
  'cleanup:release a kept simulation'"'"'s namespaces and files'
  'version:print the build version'
  'help:print usage and exit'
)

_arguments -C '1:command:->command' '*::arg:->args'

case $state in
  command)
    _describe -t commands 'netdoc-sim command' commands
    ;;
  args)
    case $words[1] in
    run)
      _arguments \
        '(--json -json)'{--json,-json}'[print the machine-readable report]' \
        '(--keep -keep)'{--keep,-keep}'[hold the network open after the report]' \
        '(--netdoc -netdoc)'{--netdoc,-netdoc}'[the netdoc binary to run]:netdoc:_files' \
        '(--timeout -timeout)'{--timeout,-timeout}'[netdoc per-probe timeout (default 4s)]:duration:' \
        '(--repeat -repeat)'{--repeat,-repeat}'[run each test n times (default 1)]:count:' \
        '(--dry-run -dry-run)'{--dry-run,-dry-run}'[print the privileged commands and stop]' \
        '(--v -v)'{--v,-v}'[log each privileged command as it runs]' \
        ':scenario:_netdoc_sim_scenarios'
      ;;
    campaign)
      _arguments \
        '(--json -json)'{--json,-json}'[print the machine-readable aggregate report]' \
        '(--runs -runs)'{--runs,-runs}'[override the campaign run count (1-1000)]:count:' \
        '(--seed -seed)'{--seed,-seed}'[root seed (generated and printed when omitted)]:int64:' \
        '(--iteration -iteration)'{--iteration,-iteration}'[run exactly one derived iteration]:index:' \
        '(--fail-fast -fail-fast)'{--fail-fast,-fail-fast}'[stop after the first mismatch or error]' \
        '(--netdoc -netdoc)'{--netdoc,-netdoc}'[the netdoc binary to run]:netdoc:_files' \
        '(--timeout -timeout)'{--timeout,-timeout}'[netdoc per-probe timeout (default 4s)]:duration:' \
        '(--v -v)'{--v,-v}'[log each privileged command as it runs]' \
        ':scenario:_netdoc_sim_scenarios'
      ;;
    hunt)
      _arguments \
        '(--json -json)'{--json,-json}'[print the machine-readable hunt report]' \
        '(--cases -cases)'{--cases,-cases}'[unique generated cases to run (default 50, max 500)]:count:' \
        '(--seed -seed)'{--seed,-seed}'[hunt seed (generated and printed when omitted)]:int64:' \
        '(--case -case)'{--case,-case}'[run exactly one derived case]:index:' \
        '(--max-faults -max-faults)'{--max-faults,-max-faults}'[maximum mutations per case (default 2, max 3)]:count:' \
        '(--fail-fast -fail-fast)'{--fail-fast,-fail-fast}'[stop after the first reportable finding]' \
        '(--dry-run -dry-run)'{--dry-run,-dry-run}'[print generated manifests without namespaces]' \
        '(--netdoc -netdoc)'{--netdoc,-netdoc}'[the netdoc binary to run]:netdoc:_files' \
        '(--timeout -timeout)'{--timeout,-timeout}'[netdoc per-probe timeout (default 4s)]:duration:' \
        '(--v -v)'{--v,-v}'[log each privileged command as it runs]' \
        ':base scenario:(dual-stack-healthy healthy healthy-routed-network socks5h-remote-dns-succeeds tls-valid two-path-healthy two-path-ipv6-healthy two-router-healthy)'
      ;;
    triage)
      _arguments \
        '(--json -json)'{--json,-json}'[print the machine-readable triage report]' \
        '(--scenarios -scenarios)'{--scenarios,-scenarios}'[comma-separated baselines (default: all)]:baselines:' \
        '(--cases -cases)'{--cases,-cases}'[unique generated cases per baseline (default 20)]:count:' \
        '(--seed -seed)'{--seed,-seed}'[override the fixed seed of every baseline]:int64:' \
        '(--max-faults -max-faults)'{--max-faults,-max-faults}'[maximum mutations per case (default 2, max 3)]:count:' \
        '(--min-severity -min-severity)'{--min-severity,-min-severity}'[lowest severity worth filing (default medium)]:level:(critical high medium low info)' \
        '(--create -create)'{--create,-create}'[file reproducible findings as GitHub issues with gh]' \
        '(--context -context)'{--context,-context}'[debugging context recorded in the issue body]:text:' \
        '(--revision -revision)'{--revision,-revision}'[commit SHA to record]:sha:' \
        '(--netdoc -netdoc)'{--netdoc,-netdoc}'[the netdoc binary to run]:netdoc:_files' \
        '(--timeout -timeout)'{--timeout,-timeout}'[netdoc per-probe timeout (default 4s)]:duration:' \
        '(--v -v)'{--v,-v}'[log each privileged command as it runs]'
      ;;
    challenge)
      _arguments \
        '(--id -id)'{--id,-id}'[replay a specific challenge]:challenge id:' \
        '(--difficulty -difficulty)'{--difficulty,-difficulty}'[draw a challenge of this difficulty]:level:(easy medium hard)' \
        '(--daily -daily)'{--daily,-daily}"[play today's challenge, or -daily=YYYY-MM-DD]" \
        '(--starter -starter)'{--starter,-starter}'[draw from a curated starter pack]:pack:(fundamentals dns service tls paths routing)' \
        '(--authored -authored)'{--authored,-authored}'[play a hand-written challenge by slug]:slug:(refused-vs-blocked-refused refused-vs-blocked-blocked reset-after-accept certificate-expired certificate-wrong-name no-default-route wrong-default-route missing-subnet-route)' \
        '(--answer -answer)'{--answer,-answer}'[submit this diagnosis without opening a shell]:diagnosis:(healthy dns_failure no_default_route wrong_default_route missing_subnet_route preferred_route_failure ipv4_failure ipv6_failure tcp_port_blocked connection_refused connection_reset tls_certificate tls_hostname_mismatch http_service proxy_failure quic_udp_blocked packet_loss)' \
        '(--give-up -give-up)'{--give-up,-give-up}'[skip straight to the answer]' \
        '(--json -json)'{--json,-json}'[print the machine-readable result (needs -answer or -give-up)]' \
        '(--netdoc -netdoc)'{--netdoc,-netdoc}'[the netdoc binary to run]:netdoc:_files' \
        '(--timeout -timeout)'{--timeout,-timeout}'[netdoc per-probe timeout (default 4s)]:duration:' \
        '(--v -v)'{--v,-v}'[log each privileged command as it runs]' \
        ':challenge id:'
      ;;
    cleanup)
      _arguments \
        '(--all -all)'{--all,-all}'[release every kept simulation]' \
        ':simulation id:'
      ;;
    validate)
      _arguments ':scenario:_netdoc_sim_scenarios'
      ;;
    starters)
      _arguments ':pack:(fundamentals dns service tls paths routing)'
      ;;
    inspect)
      _arguments ':simulation id:'
      ;;
    esac
    ;;
esac
