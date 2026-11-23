package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/user/mai/pkg/interfaces"
)

const episodicQueueSize = 64

var errEpisodicClosed = errors.New("episodic store closed")

type writeJob struct {
	entry *interfaces.MemoryEntry // nil marks a flush barrier
	done  chan struct{}
}

// EpisodicStore implements interfaces.EpisodicStore using SQLite.
// Writes go to one background writer; a full queue falls back to a synchronous
// write so events are never dropped.
type EpisodicStore struct {
	db *sql.DB

	mu       sync.Mutex // serializes queue sends with Close and guards closed
	jobs     chan writeJob
	done     chan struct{}
	closed   bool
	errMu    sync.Mutex
	writeErr error

	closeOnce sync.Once
	closeErr  error
}

func NewEpisodicStore(dbPath string) (*EpisodicStore, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Create table if not exists
	query := `
	CREATE TABLE IF NOT EXISTS events (
		id TEXT PRIMARY KEY,
		type TEXT,
		content TEXT,
		metadata TEXT,
		timestamp INTEGER
	);
	CREATE INDEX IF NOT EXISTS idx_timestamp ON events(timestamp);
	`
	_, err = db.Exec(query)
	if err != nil {
		return nil, err
	}

	s := &EpisodicStore{
		db:   db,
		jobs: make(chan writeJob, episodicQueueSize),
		done: make(chan struct{}),
	}
	go s.writeLoop()

	return s, nil
}

func (s *EpisodicStore) StoreEvent(entry interfaces.MemoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errEpisodicClosed
	}
	select {
	case s.jobs <- writeJob{entry: &entry}:
		return s.lastWriteErr()
	default:
		// Bounded queue is full: write inline while holding mu so Close
		// cannot tear down the DB underneath the write.
		return s.storeEventSync(entry)
	}
}

func (s *EpisodicStore) writeLoop() {
	defer close(s.done)
	for job := range s.jobs {
		if job.entry == nil {
			close(job.done)
			continue
		}
		if err := s.storeEventSync(*job.entry); err != nil {
			s.errMu.Lock()
			if s.writeErr == nil {
				s.writeErr = err
			}
			s.errMu.Unlock()
		}
	}
}

func (s *EpisodicStore) lastWriteErr() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.writeErr
}

// Flush waits for all writes queued before the call to reach SQLite.
func (s *EpisodicStore) Flush() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return s.lastWriteErr()
	}
	done := make(chan struct{})
	// Hold mu across the send so Close cannot close jobs underneath it.
	s.jobs <- writeJob{done: done}
	s.mu.Unlock()
	<-done
	return s.lastWriteErr()
}

func (s *EpisodicStore) storeEventSync(entry interfaces.MemoryEntry) error {
	metadataJSON, err := json.Marshal(entry.Metadata)
	if err != nil {
		return err
	}

	if entry.Timestamp == 0 {
		entry.Timestamp = time.Now().Unix()
	}

	query := `INSERT INTO events (id, type, content, metadata, timestamp) VALUES (?, ?, ?, ?, ?)`
	_, err = s.db.Exec(query, entry.ID, entry.Type, entry.Content, string(metadataJSON), entry.Timestamp)
	return err
}

func (s *EpisodicStore) QueryEvents(queryStr string, limit int) ([]interfaces.MemoryEntry, error) {
	// Queries must see writes the caller has already made (read-your-writes).
	if err := s.Flush(); err != nil {
		return nil, err
	}

	var rows *sql.Rows
	var err error

	if queryStr != "" {
		// Use FTS5 if available, fall back to LIKE on content
		rows, err = s.db.Query(
			`SELECT id, type, content, metadata, timestamp FROM events
			 WHERE content LIKE '%' || ? || '%' AND content != ''
			 ORDER BY timestamp DESC LIMIT ?`, queryStr, limit)
	} else {
		rows, err = s.db.Query(
			`SELECT id, type, content, metadata, timestamp FROM events
			 ORDER BY timestamp DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []interfaces.MemoryEntry
	for rows.Next() {
		var entry interfaces.MemoryEntry
		var metadataStr string
		err := rows.Scan(&entry.ID, &entry.Type, &entry.Content, &metadataStr, &entry.Timestamp)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal([]byte(metadataStr), &entry.Metadata)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *EpisodicStore) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.jobs)
		s.mu.Unlock()

		<-s.done
		s.closeErr = errors.Join(s.lastWriteErr(), s.db.Close())
	})
	return s.closeErr
}
