// Package report defines netdoc's stable machine-readable report contract.
package report

// Report is the stable JSON object emitted by netdoc -json. Its field names
// and status vocabulary (PASS/WARN/FAIL/SKIP/N/A) are compatibility-sensitive.
type Report struct {
	Version string `json:"version"`
	// Ts is set only under -json -watch, where the output is a stream and each
	// pass needs to say when it ran. A single report doesn't carry one, so the
	// one-shot output stays byte-identical to what it has always been.
	Ts      string  `json:"ts,omitempty"`
	Target  *Target `json:"target"` // null in generic (no-target) mode
	Checks  []Check `json:"checks"`
	Summary string  `json:"summary"`
	Verdict string  `json:"verdict"`
	// FailedStage is the id of the first check that failed, omitted when none
	// did — the one field a CI job needs to route a bug report.
	FailedStage string `json:"failed_stage,omitempty"`
	OK          bool   `json:"ok"`
}

type Target struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

type Check struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Cause      string    `json:"cause,omitempty"`
	Families   *Families `json:"address_families,omitempty"`
	Ms         int64     `json:"ms"` // wall time, truncated but floored at 1; 0 means the check never ran
	Detail     string    `json:"detail"`
	Fix        string    `json:"fix,omitempty"`
	Addrs      []string  `json:"addrs,omitempty"`
	SelectedIP string    `json:"selected_ip,omitempty"`
	Source     string    `json:"source,omitempty"`
	Iface      string    `json:"iface,omitempty"`
	Network    string    `json:"network,omitempty"`
	Portal     *Portal   `json:"portal,omitempty"`
	Attempts   []Attempt `json:"attempts,omitempty"`
}

// A family the selected --iface source has no address for was never dialed, so
// its state is empty and the key is omitted rather than serialized as "": an
// absent family reads as untested, which is what it is.
type Families struct {
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
}

type Portal struct {
	RedirectURL string `json:"redirect_url,omitempty"`
}

type Attempt struct {
	IP  string `json:"ip"`
	Ms  int64  `json:"ms"` // same flooring as Check.Ms
	Err string `json:"error,omitempty"`
}
