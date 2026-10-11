package main

import (
	"flag"
	"os"
)

// The environment keeps a PostgreSQL URL's password out of process listings.
func databaseFlag(fs *flag.FlagSet) *string {
	return fs.String("db", os.Getenv("ABOM_DB"),
		"SQLite database path or PostgreSQL URL (required; defaults to ABOM_DB)")
}
