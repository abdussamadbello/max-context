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

// RebuildDocumentsFTS repopulates the documents_fts index from the documents table.
func RebuildDocumentsFTS(db *sql.DB) error {
	_, err := db.Exec("INSERT INTO documents_fts(documents_fts) VALUES('rebuild')")
	return err
}

// RebuildAllFTS runs all FTS rebuilds.
func RebuildAllFTS(db *sql.DB) error {
	if err := RebuildFunctionsFTS(db); err != nil {
		return err
	}
	if err := RebuildTypesFTS(db); err != nil {
		return err
	}
	return RebuildDocumentsFTS(db)
}
