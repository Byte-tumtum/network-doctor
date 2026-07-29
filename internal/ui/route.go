package ui

import (
	"math"
	"strconv"
	"strings"
)

// routeQuality is deliberately destination-only. Intermediate-hop attribution
// is not reliable enough to summarize without the raw route report beside it.
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
	if j.status != JobDone || j.evicted != 0 || j.dropped != 0 {
		return routeQuality{}, false
	}
	switch j.name {
	case "mtr":
		return parseMTRRouteQuality(j.lines)
	case "pathping":
		return parsePathpingRouteQuality(j.lines)
	default:
		return routeQuality{}, false
	}
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

// parsePathpingRouteQuality reads the final Source to Here loss and RTT.
// Matching the statistics hop to the traced terminal hop keeps partial output
// from turning the last responding router into a destination summary.
func parsePathpingRouteQuality(lines []string) (quality routeQuality, ok bool) {
	traceHop, statsHop := -1, -1
	inStats := false
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 9 && fields[3] == "=" && fields[6] == "=" &&
			strings.Contains(fields[2], "/") && strings.Contains(fields[5], "/") {
			if inStats || traceHop < 1 {
				return routeQuality{}, false
			}
			inStats = true
			continue
		}
		if len(fields) < 2 {
			continue
		}
		hop, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		if !inStats {
			if hop != traceHop+1 {
				return routeQuality{}, false
			}
			traceHop = hop
			continue
		}
		if hop != statsHop+1 {
			return routeQuality{}, false
		}
		statsHop = hop
		ok = false // every later hop supersedes the previous candidate
		if len(fields) < 11 || fields[4] != "=" || fields[8] != "=" {
			continue
		}

		latencyText, hasLatency := strings.CutSuffix(strings.ToLower(fields[1]), "ms")
		lostText, hasLost := strings.CutSuffix(fields[2], "/")
		lossText, hasLoss := strings.CutSuffix(fields[5], "%")
		nodeLostText, hasNodeLost := strings.CutSuffix(fields[6], "/")
		nodeLossText, hasNodeLoss := strings.CutSuffix(fields[9], "%")
		if !hasLatency || !hasLost || !hasLoss || !hasNodeLost || !hasNodeLoss {
			continue
		}
		latency, latencyErr := strconv.ParseFloat(latencyText, 64)
		lost, lostErr := strconv.Atoi(lostText)
		sent, sentErr := strconv.Atoi(fields[3])
		loss, lossErr := strconv.ParseFloat(lossText, 64)
		nodeLost, nodeLostErr := strconv.Atoi(nodeLostText)
		nodeSent, nodeSentErr := strconv.Atoi(fields[7])
		nodeLoss, nodeLossErr := strconv.ParseFloat(nodeLossText, 64)
		if latencyErr != nil || lostErr != nil || sentErr != nil || lossErr != nil ||
			nodeLostErr != nil || nodeSentErr != nil || nodeLossErr != nil ||
			latency < 0 || lost < 0 || sent < 1 || lost > sent ||
			loss < 0 || loss > 100 || nodeLost < 0 || nodeSent < 1 || nodeLost > nodeSent ||
			nodeLoss < 0 || nodeLoss > 100 ||
			math.IsNaN(latency) || math.IsInf(latency, 0) ||
			math.IsNaN(loss) || math.IsInf(loss, 0) ||
			math.IsNaN(nodeLoss) || math.IsInf(nodeLoss, 0) {
			continue
		}
		quality, ok = routeQuality{loss: loss, latency: latency}, true
	}
	return quality, inStats && ok && statsHop == traceHop
}
