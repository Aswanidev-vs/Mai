package memory

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/user/mai/pkg/interfaces"
)

func TestEpisodicStore_AsyncWritesAreFlushed(t *testing.T) {
	defer goleak.VerifyNone(t)

	path := t.TempDir() + "/async.db"
	store, err := NewEpisodicStore(path)
	require.NoError(t, err)

	const total = 200 // exceeds the queue, exercising the synchronous fallback
	for i := 0; i < total; i++ {
		require.NoError(t, store.StoreEvent(interfaces.MemoryEntry{
			ID: fmt.Sprintf("e-%d", i), Type: "user_input", Content: fmt.Sprintf("message-%d", i),
		}))
	}
	require.NoError(t, store.Flush())

	events, err := store.QueryEvents("", total+1)
	require.NoError(t, err)
	require.Len(t, events, total)

	// A write queued after a flush must still be drained by Close.
	require.NoError(t, store.StoreEvent(interfaces.MemoryEntry{ID: "tail", Content: "tail"}))
	require.NoError(t, store.Close())

	reopened, err := NewEpisodicStore(path)
	require.NoError(t, err)
	defer reopened.Close()
	events, err = reopened.QueryEvents("", total+2)
	require.NoError(t, err)
	require.Len(t, events, total+1)
}

func TestEpisodicStore_ConcurrentAddAndClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	path := t.TempDir() + "/concurrent.db"
	store, err := NewEpisodicStore(path)
	require.NoError(t, err)

	const writers, perWriter = 8, 100
	firstWrite := make(chan struct{}, writers)
	var succeeded atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				err := store.StoreEvent(interfaces.MemoryEntry{
					ID: fmt.Sprintf("w%d-%d", w, i), Type: "user_input", Content: "concurrent",
				})
				if i == 0 {
					select {
					case firstWrite <- struct{}{}:
					default:
					}
				}
				if err == nil {
					succeeded.Add(1)
				} else {
					assert.True(t, errors.Is(err, errEpisodicClosed), "unexpected write error: %v", err)
				}
			}
		}(w)
	}
	<-firstWrite
	require.NoError(t, store.Close())
	wg.Wait()

	reopened, err := NewEpisodicStore(path)
	require.NoError(t, err)
	defer reopened.Close()
	events, err := reopened.QueryEvents("", writers*perWriter+1)
	require.NoError(t, err)
	require.Len(t, events, int(succeeded.Load()))
}

func TestEpisodicStore_CloseIsIdempotent(t *testing.T) {
	defer goleak.VerifyNone(t)

	store, err := NewEpisodicStore(t.TempDir() + "/close.db")
	require.NoError(t, err)
	require.NoError(t, store.StoreEvent(interfaces.MemoryEntry{ID: "one", Content: "x"}))

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, store.Close())
		}()
	}
	wg.Wait()

	// Once closed, callers get an explicit error rather than a silent drop.
	require.ErrorIs(t, store.StoreEvent(interfaces.MemoryEntry{ID: "two"}), errEpisodicClosed)
}
