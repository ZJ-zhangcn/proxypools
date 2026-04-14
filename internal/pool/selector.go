package pool

import (
	"math/rand"
	"sort"

	"proxypools/internal/model"
)

func SelectSequentialNext(currentNodeID int64, statuses []model.NodeRuntimeStatus) (model.NodeRuntimeStatus, bool) {
	eligible := make([]model.NodeRuntimeStatus, 0, len(statuses))
	currentIndex := -1
	for _, status := range statuses {
		if status.State != "active" || status.ManualDisabled {
			continue
		}
		if status.NodeID == currentNodeID {
			currentIndex = len(eligible)
		}
		eligible = append(eligible, status)
	}
	if len(eligible) <= 1 {
		return model.NodeRuntimeStatus{}, false
	}
	if currentIndex == -1 {
		return eligible[0], true
	}
	nextIndex := (currentIndex + 1) % len(eligible)
	if eligible[nextIndex].NodeID == currentNodeID {
		return model.NodeRuntimeStatus{}, false
	}
	return eligible[nextIndex], true
}

func SelectRandomNext(currentNodeID int64, statuses []model.NodeRuntimeStatus) (model.NodeRuntimeStatus, bool) {
	eligible := make([]model.NodeRuntimeStatus, 0, len(statuses))
	for _, status := range statuses {
		if status.State != "active" || status.ManualDisabled || status.NodeID == currentNodeID {
			continue
		}
		eligible = append(eligible, status)
	}
	if len(eligible) == 0 {
		return model.NodeRuntimeStatus{}, false
	}
	return eligible[rand.Intn(len(eligible))], true
}

func SelectBalanceNext(currentNodeID int64, statuses []model.NodeRuntimeStatus) (model.NodeRuntimeStatus, bool) {
	return SelectNext(currentNodeID, statuses)
}

func SelectNext(currentNodeID int64, statuses []model.NodeRuntimeStatus) (model.NodeRuntimeStatus, bool) {
	byTier := map[string][]model.NodeRuntimeStatus{"L1": {}, "L2": {}, "L3": {}}
	currentTier := "L3"

	for _, status := range statuses {
		if status.NodeID == currentNodeID {
			currentTier = status.Tier
		}
		if status.State == "active" && !status.ManualDisabled {
			byTier[status.Tier] = append(byTier[status.Tier], status)
		}
	}

	order := []string{currentTier, "L1", "L2", "L3"}
	seen := map[string]bool{}
	for _, tier := range order {
		if seen[tier] {
			continue
		}
		seen[tier] = true
		candidates := byTier[tier]
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Score > candidates[j].Score
		})
		for _, candidate := range candidates {
			if candidate.NodeID != currentNodeID {
				return candidate, true
			}
		}
	}

	return model.NodeRuntimeStatus{}, false
}
