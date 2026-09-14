package main

import "context"

// App is the Wails window host and nothing more.
//
// The desktop app's Go side does not touch the database (CLAUDE.md §3): all
// business logic lives in internal/stockroom behind the localhost HTTP server,
// and this frontend reaches it with fetch exactly as the web app does. Binding
// methods here would be a second API surface that only one of the two hosts
// could call, which is how the two UIs start to drift.
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct.
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved so we can call
// the Wails runtime methods.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}
