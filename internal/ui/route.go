package ui

import (
	"math"
	"strconv"
	"strings"
)

// routeQuality is deliberately destination-only. Intermediate-hop attribution
// is not reliable enough to summarize without the raw mtr report beside it.
type routeQuality struct {
	loss, latency float64
}

func (q routeQuality) String() string {
	return "destination: " + routeNumber(q.loss) + "% loss · " +
		routeNumber(q.latency) + "ms avg"
}

func routeNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (j jobState) routeQuality() (routeQuality, bool) {
	if j.name != "mtr" || j.status != JobDone || j.evicted != 0 || j.dropped != 0 {
		return routeQuality{}, false
	}
	return parseMTRRouteQuality(j.lines)
}

// parseMTRRouteQuality reads the last hop from the pinned LSNABWV report
// columns. A final ??? row means mtr did not prove it reached the destination,
// so no destination facts are emitted.
func parseMTRRouteQuality(lines []string) (quality routeQuality, ok bool) {
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 9 {
			continue
		}
		hop, found := strings.CutSuffix(fields[0], ".|--")
		if !found {
			continue
		}
		if _, err := strconv.Atoi(hop); err != nil {
			continue
		}

		ok = false // every later hop supersedes the previous candidate
		if fields[1] == "???" {
			continue
		}
		lossText, found := strings.CutSuffix(fields[2], "%")
		if !found {
			continue
		}
		loss, lossErr := strconv.ParseFloat(lossText, 64)
		sent, sentErr := strconv.Atoi(fields[3])
		latency, latencyErr := strconv.ParseFloat(fields[5], 64)
		if lossErr != nil || sentErr != nil || latencyErr != nil || sent < 1 ||
			loss < 0 || loss > 100 || latency < 0 ||
			math.IsNaN(loss) || math.IsInf(loss, 0) ||
			math.IsNaN(latency) || math.IsInf(latency, 0) {
			continue
		}
		quality, ok = routeQuality{loss: loss, latency: latency}, true
	}
	return quality, ok
}
