package main

import (
	"flag"
	"os"
)

// The environment keeps a PostgreSQL URL's password out of process listings.
func databaseFlag(fs *flag.FlagSet) *string {
	return envFlag(fs, "db", "ABOM_DB",
		"SQLite database path or PostgreSQL URL (required; defaults to ABOM_DB)")
}

// envFlag is a string flag the environment variable env supplies when it is not given. The value is
// assigned after the flag is defined, so -h never prints it as the default: a URL carries its
// password, and help is pasted into issues.
func envFlag(fs *flag.FlagSet, name, env, usage string) *string {
	value := fs.String(name, "", usage)
	*value = os.Getenv(env)
	return value
}
