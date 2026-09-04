// Package stockroom holds all of Stockroom's business logic: database access,
// accounts and sessions, asset browsing, checkout/check-in, admin operations,
// and backup. It is the only package that talks to Postgres.
//
// Callers (server/ and cmd/) stay thin: they translate HTTP or CLI input into
// calls on this package and map the sentinel errors in errors.go back to
// status codes or exit codes. See CLAUDE.md §4 for the architecture.
package stockroom
