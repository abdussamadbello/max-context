package indexer

import (
	"database/sql"
	"sync"
)

// Resolver maps function names to IDs for resolving callee_id in calls.
type Resolver struct {
	mu   sync.RWMutex
	byName map[string][]int64
}

// NewResolver builds a name -> []functionID index from the database.
func NewResolver(db *sql.DB) (*Resolver, error) {
	rows, err := db.Query("SELECT id, name FROM functions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := make(map[string][]int64)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		byName[name] = append(byName[name], id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &Resolver{byName: byName}, nil
}

// Resolve returns the first matching function ID for the given name, or 0 if not found.
func (r *Resolver) Resolve(name string) int64 {
	r.mu.RLock()
	ids := r.byName[name]
	r.mu.RUnlock()
	if len(ids) == 0 {
		return 0
	}
	return ids[0]
}

// Add records a new function ID for name (used after insert).
func (r *Resolver) Add(name string, id int64) {
	r.mu.Lock()
	r.byName[name] = append(r.byName[name], id)
	r.mu.Unlock()
}

// Remove removes an ID from the name bucket (used when reindexing a file).
func (r *Resolver) Remove(name string, id int64) {
	r.mu.Lock()
	ids := r.byName[name]
	for i, v := range ids {
		if v == id {
			r.byName[name] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
	r.mu.Unlock()
}

// CallerForLine returns the function ID that contains the given line in the file, or 0.
func CallerForLine(db *sql.DB, filePath string, line int) (int64, error) {
	return callerForLineQuery(db, filePath, line)
}

// CallerForLineTx is like CallerForLine but runs in the given transaction.
func CallerForLineTx(tx *sql.Tx, filePath string, line int) (int64, error) {
	return callerForLineQuery(tx, filePath, line)
}

type queryRower interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}

func callerForLineQuery(q queryRower, filePath string, line int) (int64, error) {
	var id int64
	err := q.QueryRow(
		"SELECT id FROM functions WHERE file_path = ? AND start_line <= ? AND end_line >= ? LIMIT 1",
		filePath, line, line,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}
