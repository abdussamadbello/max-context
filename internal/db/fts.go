package db

import "database/sql"

// RebuildFunctionsFTS repopulates the functions_fts index from the functions table.
// Call after bulk insert/delete (e.g. full or incremental reindex).
func RebuildFunctionsFTS(db *sql.DB) error {
	_, err := db.Exec("INSERT INTO functions_fts(functions_fts) VALUES('rebuild')")
	return err
}

// RebuildTypesFTS repopulates the types_fts index from the types table.
func RebuildTypesFTS(db *sql.DB) error {
	_, err := db.Exec("INSERT INTO types_fts(types_fts) VALUES('rebuild')")
	return err
}

// RebuildAllFTS runs both FTS rebuilds.
func RebuildAllFTS(db *sql.DB) error {
	if err := RebuildFunctionsFTS(db); err != nil {
		return err
	}
	return RebuildTypesFTS(db)
}
