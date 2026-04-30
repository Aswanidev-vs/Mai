package memory

import (
	"database/sql"
	"encoding/json"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/user/mai/pkg/interfaces"
)

// EpisodicStore implements interfaces.EpisodicStore using SQLite
type EpisodicStore struct {
	db *sql.DB
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

	return &EpisodicStore{db: db}, nil
}

func (s *EpisodicStore) StoreEvent(entry interfaces.MemoryEntry) error {
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
	// Simple query for now, ignoring queryStr and just returning latest events
	// In a real implementation, we would use FTS5 for queryStr searching
	rows, err := s.db.Query(`SELECT id, type, content, metadata, timestamp FROM events ORDER BY timestamp DESC LIMIT ?`, limit)
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
	return s.db.Close()
}
