package pool

import (
	"hash/fnv"
	"math/rand"
	"sort"
)

type DispatcherCandidate struct {
	PortKey            string
	LaneKey            string
	Protocol           string
	Weight             int
	HealthyNodeCount   int
	CurrentActiveScore float64
	CurrentActiveSet   bool
	LastFailureAt      string
}

func SelectSequentialDispatcherPort(currentPortKey string, candidates []DispatcherCandidate) (DispatcherCandidate, bool) {
	eligible := make([]DispatcherCandidate, 0, len(candidates))
	currentIndex := -1
	for _, candidate := range candidates {
		if candidate.HealthyNodeCount <= 0 {
			continue
		}
		if candidate.PortKey == currentPortKey {
			currentIndex = len(eligible)
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		return DispatcherCandidate{}, false
	}
	if len(eligible) == 1 {
		return eligible[0], true
	}
	if currentIndex == -1 {
		return eligible[0], true
	}
	nextIndex := (currentIndex + 1) % len(eligible)
	return eligible[nextIndex], true
}

func SelectRandomDispatcherPort(currentPortKey string, candidates []DispatcherCandidate) (DispatcherCandidate, bool) {
	eligible := make([]DispatcherCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.HealthyNodeCount <= 0 {
			continue
		}
		if len(candidates) > 1 && candidate.PortKey == currentPortKey {
			continue
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		for _, candidate := range candidates {
			if candidate.HealthyNodeCount > 0 {
				eligible = append(eligible, candidate)
			}
		}
	}
	if len(eligible) == 0 {
		return DispatcherCandidate{}, false
	}
	return eligible[rand.Intn(len(eligible))], true
}

func SelectBalanceDispatcherPort(currentPortKey string, candidates []DispatcherCandidate) (DispatcherCandidate, bool) {
	eligible := make([]DispatcherCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.HealthyNodeCount <= 0 {
			continue
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		return DispatcherCandidate{}, false
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].HealthyNodeCount != eligible[j].HealthyNodeCount {
			return eligible[i].HealthyNodeCount > eligible[j].HealthyNodeCount
		}
		if eligible[i].Weight != eligible[j].Weight {
			return eligible[i].Weight > eligible[j].Weight
		}
		if eligible[i].CurrentActiveScore != eligible[j].CurrentActiveScore {
			return eligible[i].CurrentActiveScore > eligible[j].CurrentActiveScore
		}
		if eligible[i].CurrentActiveSet != eligible[j].CurrentActiveSet {
			return eligible[i].CurrentActiveSet
		}
		if eligible[i].LastFailureAt != eligible[j].LastFailureAt {
			return eligible[i].LastFailureAt < eligible[j].LastFailureAt
		}
		if eligible[i].PortKey != eligible[j].PortKey {
			return eligible[i].PortKey < eligible[j].PortKey
		}
		return eligible[i].LaneKey < eligible[j].LaneKey
	})
	for _, candidate := range eligible {
		if candidate.PortKey != currentPortKey {
			return candidate, true
		}
	}
	return eligible[0], true
}

func SelectSequentialDispatcherLane(currentLaneKey string, candidates []DispatcherCandidate) (DispatcherCandidate, bool) {
	eligible := make([]DispatcherCandidate, 0, len(candidates))
	currentIndex := -1
	for _, candidate := range candidates {
		if candidate.HealthyNodeCount <= 0 {
			continue
		}
		if dispatcherLaneSelectionKey(candidate) == currentLaneKey || candidate.LaneKey == currentLaneKey {
			currentIndex = len(eligible)
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		return DispatcherCandidate{}, false
	}
	if len(eligible) == 1 {
		return eligible[0], true
	}
	if currentIndex == -1 {
		return eligible[0], true
	}
	nextIndex := (currentIndex + 1) % len(eligible)
	return eligible[nextIndex], true
}

func SelectRandomDispatcherLane(currentLaneKey string, candidates []DispatcherCandidate) (DispatcherCandidate, bool) {
	eligible := make([]DispatcherCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.HealthyNodeCount <= 0 {
			continue
		}
		if candidate.Weight <= 0 {
			candidate.Weight = 1
		}
		if len(candidates) > 1 && (dispatcherLaneSelectionKey(candidate) == currentLaneKey || candidate.LaneKey == currentLaneKey) {
			continue
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		for _, candidate := range candidates {
			if candidate.HealthyNodeCount > 0 {
				if candidate.Weight <= 0 {
					candidate.Weight = 1
				}
				eligible = append(eligible, candidate)
			}
		}
	}
	if len(eligible) == 0 {
		return DispatcherCandidate{}, false
	}
	totalWeight := 0
	for _, candidate := range eligible {
		totalWeight += candidate.Weight
	}
	pick := rand.Intn(totalWeight)
	for _, candidate := range eligible {
		pick -= candidate.Weight
		if pick < 0 {
			return candidate, true
		}
	}
	return eligible[len(eligible)-1], true
}

func SelectBalanceDispatcherLane(currentLaneKey string, candidates []DispatcherCandidate) (DispatcherCandidate, bool) {
	eligible := make([]DispatcherCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.HealthyNodeCount <= 0 {
			continue
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		return DispatcherCandidate{}, false
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].HealthyNodeCount != eligible[j].HealthyNodeCount {
			return eligible[i].HealthyNodeCount > eligible[j].HealthyNodeCount
		}
		if eligible[i].Weight != eligible[j].Weight {
			return eligible[i].Weight > eligible[j].Weight
		}
		if eligible[i].CurrentActiveScore != eligible[j].CurrentActiveScore {
			return eligible[i].CurrentActiveScore > eligible[j].CurrentActiveScore
		}
		if eligible[i].CurrentActiveSet != eligible[j].CurrentActiveSet {
			return eligible[i].CurrentActiveSet
		}
		if eligible[i].LastFailureAt != eligible[j].LastFailureAt {
			return eligible[i].LastFailureAt < eligible[j].LastFailureAt
		}
		return dispatcherLaneSelectionKey(eligible[i]) < dispatcherLaneSelectionKey(eligible[j])
	})
	for _, candidate := range eligible {
		if dispatcherLaneSelectionKey(candidate) != currentLaneKey && candidate.LaneKey != currentLaneKey {
			return candidate, true
		}
	}
	return eligible[0], true
}

func SelectStickyDispatcherLane(stickyKey string, candidates []DispatcherCandidate) (DispatcherCandidate, bool) {
	eligible := make([]DispatcherCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.HealthyNodeCount <= 0 {
			continue
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		return DispatcherCandidate{}, false
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		return dispatcherLaneSelectionKey(eligible[i]) < dispatcherLaneSelectionKey(eligible[j])
	})
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(stickyKey))
	index := int(hasher.Sum32()) % len(eligible)
	return eligible[index], true
}

func dispatcherLaneSelectionKey(candidate DispatcherCandidate) string {
	return candidate.PortKey + ":" + candidate.LaneKey + ":" + candidate.Protocol
}
