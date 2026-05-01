package agent

import (
	"container/heap"
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/user/mai/pkg/interfaces"
)

type GoalState struct {
	Goal      interfaces.Goal
	CreatedAt time.Time
	StartedAt *time.Time
	SubTasks  []string
	Results   []string
}

type goalHeap []*GoalState

func (h goalHeap) Len() int            { return len(h) }
func (h goalHeap) Less(i, j int) bool  { return h[i].Goal.Priority > h[j].Goal.Priority }
func (h goalHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *goalHeap) Push(x interface{}) { *h = append(*h, x.(*GoalState)) }
func (h *goalHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

type GoalManager struct {
	mu        sync.RWMutex
	queue     goalHeap
	active    map[string]*GoalState
	completed map[string]*GoalState
	failed    map[string]*GoalState
}

func NewGoalManager() *GoalManager {
	gm := &GoalManager{
		active:    make(map[string]*GoalState),
		completed: make(map[string]*GoalState),
		failed:    make(map[string]*GoalState),
	}
	heap.Init(&gm.queue)
	return gm
}

func (gm *GoalManager) AddGoal(goal interfaces.Goal) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	state := &GoalState{
		Goal:      goal,
		CreatedAt: time.Now(),
	}

	if goal.Status == "" {
		state.Goal.Status = "pending"
	}

	heap.Push(&gm.queue, state)
	log.Printf("[GoalManager] Added goal: %s (priority: %d)", goal.Description, goal.Priority)
}

func (gm *GoalManager) GetNext() *GoalState {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	if gm.queue.Len() == 0 {
		return nil
	}

	state := heap.Pop(&gm.queue).(*GoalState)
	now := time.Now()
	state.StartedAt = &now
	state.Goal.Status = "active"
	gm.active[state.Goal.ID] = state

	return state
}

func (gm *GoalManager) CompleteGoal(id string, result string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	if state, ok := gm.active[id]; ok {
		state.Goal.Status = "completed"
		state.Results = append(state.Results, result)
		gm.completed[id] = state
		delete(gm.active, id)
		log.Printf("[GoalManager] Completed goal: %s", state.Goal.Description)
	}
}

func (gm *GoalManager) FailGoal(id string, reason string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	if state, ok := gm.active[id]; ok {
		state.Goal.Status = "failed"
		state.Results = append(state.Results, "FAILED: "+reason)
		gm.failed[id] = state
		delete(gm.active, id)
		log.Printf("[GoalManager] Failed goal: %s — %s", state.Goal.Description, reason)
	}
}

func (gm *GoalManager) GetActive() []*GoalState {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	var goals []*GoalState
	for _, g := range gm.active {
		goals = append(goals, g)
	}
	return goals
}

func (gm *GoalManager) GetPendingCount() int {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return gm.queue.Len()
}

func (gm *GoalManager) HasActiveGoals() bool {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return len(gm.active) > 0
}

func (gm *GoalManager) ExecutePlan(ctx context.Context, plan []interfaces.Goal, execFn func(ctx context.Context, goal interfaces.Goal) (string, error)) error {
	for _, goal := range plan {
		gm.AddGoal(goal)
	}

	for gm.GetPendingCount() > 0 || gm.HasActiveGoals() {
		next := gm.GetNext()
		if next == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		result, err := execFn(ctx, next.Goal)
		if err != nil {
			gm.FailGoal(next.Goal.ID, err.Error())
			continue
		}
		gm.CompleteGoal(next.Goal.ID, result)
	}

	return nil
}

func (gm *GoalManager) Stats() string {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return fmt.Sprintf("pending=%d active=%d completed=%d failed=%d",
		gm.queue.Len(), len(gm.active), len(gm.completed), len(gm.failed))
}
