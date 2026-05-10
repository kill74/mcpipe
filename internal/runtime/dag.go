package runtime

import (
	"fmt"
	"sort"
	"strings"

	"mcpipe/internal/config"
)

func Levels(steps []config.Step) ([][]config.Step, error) {
	byID := map[string]config.Step{}
	dependents := map[string][]string{}
	remaining := map[string]int{}
	for _, step := range steps {
		byID[step.ID] = step
		remaining[step.ID] = len(step.DependsOn)
		for _, dep := range step.DependsOn {
			dependents[dep] = append(dependents[dep], step.ID)
		}
	}
	var ready []string
	for id, count := range remaining {
		if count == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	var levels [][]config.Step
	visited := 0
	for len(ready) > 0 {
		currentIDs := append([]string(nil), ready...)
		ready = nil
		level := make([]config.Step, 0, len(currentIDs))
		for _, id := range currentIDs {
			level = append(level, byID[id])
			visited++
			for _, child := range dependents[id] {
				remaining[child]--
				if remaining[child] == 0 {
					ready = append(ready, child)
				}
			}
		}
		sort.Slice(level, func(i, j int) bool { return level[i].ID < level[j].ID })
		sort.Strings(ready)
		levels = append(levels, level)
	}
	if visited != len(steps) {
		var blocked []string
		for id, count := range remaining {
			if count > 0 {
				blocked = append(blocked, id)
			}
		}
		sort.Strings(blocked)
		return nil, fmt.Errorf("dependency cycle or missing dependency involving: %s", strings.Join(blocked, ", "))
	}
	return levels, nil
}
