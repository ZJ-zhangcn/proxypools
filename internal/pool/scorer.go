package pool

import "proxypools/internal/model"

type ScoredStatus struct {
	model.NodeRuntimeStatus
}

func Score(in model.NodeRuntimeStatus) ScoredStatus {
	score := 100.0
	score -= float64(in.LatencyMS) / 20
	score += in.RecentSuccessRate * 20
	score -= float64(in.ConsecutiveFailures * 15)
	in.Score = score

	switch {
	case score >= 90:
		in.Tier = "L1"
	case score >= 70:
		in.Tier = "L2"
	default:
		in.Tier = "L3"
	}

	if in.ConsecutiveFailures >= 3 {
		in.State = "cooldown"
	} else if in.State == "" {
		in.State = "active"
	}

	return ScoredStatus{NodeRuntimeStatus: in}
}
