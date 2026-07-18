package postgres

import (
	"context"
	"errors"
	"time"
)

// StorageInspection is a read-only, sanitized PostgreSQL maintenance report.
type StorageInspection struct {
	DatabaseBytes    int64             `json:"database_bytes"`
	MaxDatabaseBytes int64             `json:"max_database_bytes"`
	EvidenceCount    int64             `json:"evidence_count"`
	BytesPerEvidence int64             `json:"bytes_per_evidence"`
	Relations        []RelationStorage `json:"relations"`
	Indexes          []IndexStorage    `json:"indexes"`
}

// RelationStorage describes table size, tuple health, and maintenance recency.
type RelationStorage struct {
	Name        string     `json:"name"`
	TableBytes  int64      `json:"table_bytes"`
	IndexBytes  int64      `json:"index_bytes"`
	TotalBytes  int64      `json:"total_bytes"`
	LiveTuples  int64      `json:"live_tuples"`
	DeadTuples  int64      `json:"dead_tuples"`
	LastVacuum  *time.Time `json:"last_vacuum,omitempty"`
	LastAnalyze *time.Time `json:"last_analyze,omitempty"`
}

// IndexStorage describes index size and observed scans.
type IndexStorage struct {
	TableName string `json:"table_name"`
	IndexName string `json:"index_name"`
	Bytes     int64  `json:"bytes"`
	Scans     int64  `json:"scans"`
	Unused    bool   `json:"unused"`
}

// InspectStorage reports maintenance facts without changing database state.
func (r *Repository) InspectStorage(ctx context.Context) (StorageInspection, error) {
	result := StorageInspection{
		MaxDatabaseBytes: r.maxDatabaseBytes,
		Relations:        []RelationStorage{},
		Indexes:          []IndexStorage{},
	}
	if err := r.pool.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&result.DatabaseBytes); err != nil {
		return StorageInspection{}, errors.New("inspect database size")
	}
	rows, err := r.pool.Query(ctx, `SELECT relname,
		pg_relation_size(relid), pg_indexes_size(relid), pg_total_relation_size(relid),
		n_live_tup, n_dead_tup, last_vacuum, last_analyze
		FROM pg_stat_user_tables WHERE schemaname='prompt_better' ORDER BY relname`)
	if err != nil {
		return StorageInspection{}, errors.New("inspect relation storage")
	}
	for rows.Next() {
		var relation RelationStorage
		if err := rows.Scan(&relation.Name, &relation.TableBytes, &relation.IndexBytes,
			&relation.TotalBytes, &relation.LiveTuples, &relation.DeadTuples,
			&relation.LastVacuum, &relation.LastAnalyze); err != nil {
			rows.Close()
			return StorageInspection{}, errors.New("scan relation storage")
		}
		result.Relations = append(result.Relations, relation)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return StorageInspection{}, errors.New("iterate relation storage")
	}
	rows.Close()
	var schemaBytes int64
	for _, relation := range result.Relations {
		schemaBytes += relation.TotalBytes
	}
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM prompt_better.evidence_artifacts`).
		Scan(&result.EvidenceCount); err != nil {
		return StorageInspection{}, errors.New("inspect evidence count")
	}
	if result.EvidenceCount > 0 {
		result.BytesPerEvidence = schemaBytes / result.EvidenceCount
	}

	rows, err = r.pool.Query(ctx, `SELECT relname, indexrelname,
		pg_relation_size(indexrelid), idx_scan
		FROM pg_stat_user_indexes WHERE schemaname='prompt_better'
		ORDER BY relname, indexrelname`)
	if err != nil {
		return StorageInspection{}, errors.New("inspect index storage")
	}
	defer rows.Close()
	for rows.Next() {
		var index IndexStorage
		if err := rows.Scan(&index.TableName, &index.IndexName, &index.Bytes, &index.Scans); err != nil {
			return StorageInspection{}, errors.New("scan index storage")
		}
		index.Unused = index.Scans == 0
		result.Indexes = append(result.Indexes, index)
	}
	if err := rows.Err(); err != nil {
		return StorageInspection{}, errors.New("iterate index storage")
	}
	return result, nil
}
