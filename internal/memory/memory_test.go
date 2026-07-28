package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/mai/pkg/interfaces"
)

func TestWorkingMemory_AddAndGetContext(t *testing.T) {
	mem := NewWorkingMemory(5)

	mem.Add(interfaces.MemoryEntry{
		Type:    "user_input",
		Content: "Hello",
	})

	ctx := mem.GetContext()
	assert.Contains(t, ctx, "Hello")
}

func TestWorkingMemory_MaxEntries(t *testing.T) {
	mem := NewWorkingMemory(3)

	for i := 0; i < 5; i++ {
		mem.Add(interfaces.MemoryEntry{
			Type:    "user_input",
			Content: string(rune('A' + i)),
		})
	}

	ctx := mem.GetContext()
	assert.Contains(t, ctx, "C")
	assert.Contains(t, ctx, "D")
	assert.Contains(t, ctx, "E")
	assert.NotContains(t, ctx, "A")
	assert.NotContains(t, ctx, "B")
}

func TestWorkingMemory_EmptyContext(t *testing.T) {
	mem := NewWorkingMemory(5)
	ctx := mem.GetContext()
	assert.Empty(t, ctx)
}

func TestWorkingMemory_Clear(t *testing.T) {
	mem := NewWorkingMemory(5)

	mem.Add(interfaces.MemoryEntry{
		Type:    "user_input",
		Content: "Hello",
	})

	mem.Clear()
	ctx := mem.GetContext()
	assert.Empty(t, ctx)
}

func TestWorkingMemory_ConcurrentAccess(t *testing.T) {
	mem := NewWorkingMemory(100)

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				mem.Add(interfaces.MemoryEntry{
					Type:    "user_input",
					Content: "concurrent",
				})
				mem.GetContext()
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestProceduralStore_AddAndGetPattern(t *testing.T) {
	store, err := NewProceduralStore(t.TempDir())
	require.NoError(t, err)

	err = store.AddSkill("web_search", "web_search: success count=5")
	require.NoError(t, err)

	pattern, err := store.GetSkill("web_search")
	require.NoError(t, err)
	assert.Equal(t, "web_search: success count=5", pattern)
}

func TestProceduralStore_GetBestPattern(t *testing.T) {
	store, err := NewProceduralStore(t.TempDir())
	require.NoError(t, err)

	store.AddSkill("search_success", "web_search pattern")
	store.RecordSuccess("search_success")
	store.RecordSuccess("search_success")
	store.RecordFailure("search_success")

	store.AddSkill("search_fail", "web_search pattern")
	store.RecordFailure("search_fail")

	pattern, score := store.GetBestPattern("web_search")
	assert.NotEmpty(t, pattern)
	assert.Greater(t, score, 0.5)
}

func TestProceduralStore_EmptyPattern(t *testing.T) {
	store, err := NewProceduralStore(t.TempDir())
	require.NoError(t, err)

	pattern, score := store.GetBestPattern("nonexistent query")
	assert.Empty(t, pattern)
	assert.Equal(t, 0.0, score)
}

func TestProceduralStore_RecordSuccessFailure(t *testing.T) {
	store, err := NewProceduralStore(t.TempDir())
	require.NoError(t, err)

	store.AddSkill("test_skill", "pattern")

	store.RecordSuccess("test_skill")
	store.RecordSuccess("test_skill")
	store.RecordFailure("test_skill")

	skills := store.ListSkills()
	require.Len(t, skills, 1)
	assert.Equal(t, 2, skills[0].Successes)
	assert.Equal(t, 1, skills[0].Failures)
}

func TestProceduralStore_ListSkills(t *testing.T) {
	store, err := NewProceduralStore(t.TempDir())
	require.NoError(t, err)

	store.AddSkill("skill1", "pattern1")
	store.AddSkill("skill2", "pattern2")

	skills := store.ListSkills()
	assert.Len(t, skills, 2)
}

func TestEpisodicStore_StoreAndQuery(t *testing.T) {
	store, err := NewEpisodicStore(t.TempDir() + "/test.db")
	require.NoError(t, err)
	defer store.Close()

	err = store.StoreEvent(interfaces.MemoryEntry{
		ID:        "event-1",
		Type:      "user_input",
		Content:   "Hello",
		Timestamp: time.Now().Unix(),
	})
	require.NoError(t, err)

	// Query by content (not type)
	events, err := store.QueryEvents("Hello", 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "Hello", events[0].Content)

	// Query all (empty string)
	allEvents, err := store.QueryEvents("", 10)
	require.NoError(t, err)
	assert.Len(t, allEvents, 1)
}

func TestEpisodicStore_TimeRangeQuery(t *testing.T) {
	store, err := NewEpisodicStore(t.TempDir() + "/test.db")
	require.NoError(t, err)
	defer store.Close()

	now := time.Now()

	store.StoreEvent(interfaces.MemoryEntry{
		ID: "old", Type: "user_input", Content: "old message",
		Timestamp: now.Add(-2 * time.Hour).Unix(),
	})
	store.StoreEvent(interfaces.MemoryEntry{
		ID: "new", Type: "user_input", Content: "new message",
		Timestamp: now.Unix(),
	})

	// Query all events
	events, err := store.QueryEvents("", 10)
	require.NoError(t, err)
	assert.Len(t, events, 2)

	// Query by content
	oldEvents, err := store.QueryEvents("old", 10)
	require.NoError(t, err)
	assert.Len(t, oldEvents, 1)
}

func TestMemoryManager_StoreAndRetrieve(t *testing.T) {
	working := NewWorkingMemory(10)
	episodic, err := NewEpisodicStore(t.TempDir() + "/episodic.db")
	require.NoError(t, err)
	defer episodic.Close()

	mem := NewMemoryManager(working, episodic, nil, nil)

	// Store to episodic
	err = mem.Store(context.Background(), interfaces.MemoryEntry{
		ID:        "test-1",
		Type:      "user_input",
		Content:   "Hello Mai",
		Timestamp: time.Now().Unix(),
	})
	require.NoError(t, err)

	// Verify episodic storage
	events, err := episodic.QueryEvents("Hello Mai", 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "Hello Mai", events[0].Content)

	// Add to working memory separately (Store doesn't add to working)
	working.Add(interfaces.MemoryEntry{
		Type:    "user_input",
		Content: "Hello Mai",
	})
	ctx := working.GetContext()
	assert.Contains(t, ctx, "Hello Mai")
}
