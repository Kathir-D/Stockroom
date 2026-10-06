// Package wizard is the graphical installer's window: one page in the
// machine's own browser, served on loopback for the length of one install.
// `stockroom setup --gui` (Linux and macOS) and Stockroom-Setup.exe (Windows)
// both fill in a Wizard and call Serve, so the three installers look and
// behave alike and none needs a GUI toolkit.
//
// The package knows nothing about installing. A Wizard carries the questions
// to ask and a Run function, and the page shows whatever Run writes: a line
// of the form "== Name ==" moves the step list on, which is how setup has
// always announced its steps.
package wizard

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed page.html
var page []byte

// Answers is what the person typed into the page.
type Answers struct {
	AdminNumber   string `json:"adminNumber"`
	AdminPassword string `json:"adminPassword"`
	WithCamera    bool   `json:"withCamera"`
}

// Need is something the machine lacks that the installer can't supply. The
// page shows it instead of the form, with a button that checks again.
type Need struct {
	Message  string `json:"message"`
	LinkText string `json:"linkText,omitempty"`
	LinkURL  string `json:"linkUrl,omitempty"`
}

// Result is how a finished install ends.
type Result struct {
	// URL is where Stockroom now answers. The page offers to open it.
	URL string
	// Message is shown above that, or alone when there is no URL: an install
	// that stopped for a restart has nothing to open yet.
	Message string
}

// Wizard is one installer.
type Wizard struct {
	// Platform names the machine in the heading: "Linux", "macOS", "Windows".
	Platform string
	// Steps are the step names, in the order Run announces them.
	Steps []string
	// Notes say what the install will do to this machine.
	Notes []string
	// AskAdmin shows the failsafe admin's two fields. AdminNote replaces them
	// when there is nothing to ask.
	AskAdmin  bool
	AdminNote string
	// Camera offers the closet camera.
	Camera bool

	// Need is asked each time the page loads its state. Nil means nothing is
	// ever missing.
	Need func() *Need
	// Check refuses answers before anything is installed.
	Check func(Answers) error
	// Run installs. What it writes to out is the page's log.
	Run func(ctx context.Context, a Answers, out io.Writer) (Result, error)

	// Open opens the page in a browser. Nil prints the address only.
	Open func(url string) error
	// Log is where the page's address is printed, on a line of its own, so a
	// launcher that started the wizard as another user can open it.
	Log io.Writer
	// Idle is how long the wizard waits for a page that has gone away before
	// it exits. Zero means three minutes, which outlasts a browser's slowest
	// timer in a background tab.
	Idle time.Duration
}

const (
	phaseAsk     = "ask"
	phaseRunning = "running"
	phaseDone    = "done"
	phaseFailed  = "failed"
)

// session is one Serve: the page's token and what the install has said.
type session struct {
	w     *Wizard
	token string
	idle  time.Duration
	quit  chan struct{}
	once  sync.Once

	mu       sync.Mutex
	phase    string
	lines    []string
	partial  string
	step     int // index into Steps of the step running, -1 before the first
	err      string
	result   Result
	lastSeen time.Time
}

// Serve shows the wizard and returns when the page says it is finished, when
// the page has been gone for Idle with no install running, or when ctx ends.
func (w *Wizard) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	s := &session{
		w:        w,
		token:    hex.EncodeToString(raw),
		idle:     w.Idle,
		quit:     make(chan struct{}),
		phase:    phaseAsk,
		step:     -1,
		lastSeen: time.Now(),
	}
	if s.idle == 0 {
		s.idle = 3 * time.Minute
	}
	host := ln.Addr().String()
	url := "http://" + host + "/?t=" + s.token

	// An install outlives a cancelled request, but not the wizard.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	srv := &http.Server{Handler: s.routes(runCtx, host), ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()

	if w.Log != nil {
		fmt.Fprintf(w.Log, "Stockroom Setup runs in your browser, at this address:\n%s\n", url)
	}
	if w.Open != nil {
		if err := w.Open(url); err != nil && w.Log != nil {
			fmt.Fprintf(w.Log, "could not open a browser: %v\n", err)
		}
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case err := <-served:
			return err
		case <-ctx.Done():
		case <-s.quit:
		case <-tick.C:
			if !s.abandoned() {
				continue
			}
		}
		break
	}
	shut, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	_ = srv.Shutdown(shut)
	return ctx.Err()
}

// abandoned reports whether the page has stopped asking and nothing is being
// installed. A running install is never abandoned: closing the tab halfway
// must not leave a machine half set up.
func (s *session) abandoned() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase != phaseRunning && time.Since(s.lastSeen) > s.idle
}

func (s *session) routes(runCtx context.Context, host string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-store")
		rw.Header().Set("Referrer-Policy", "no-referrer")
		rw.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'")
		_, _ = rw.Write(page)
	})
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/run", func(rw http.ResponseWriter, r *http.Request) { s.handleRun(runCtx, rw, r) })
	mux.HandleFunc("POST /api/bye", s.handleBye)
	mux.HandleFunc("POST /api/quit", func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusNoContent)
		s.once.Do(func() { close(s.quit) })
	})

	_, port, _ := net.SplitHostPort(host)
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// The wizard takes a password and installs software, as root on
		// Linux. A page on another site must not reach it: the Host check
		// stops DNS rebinding, and the token stops everything else on this
		// machine that can guess a port.
		if r.Host != host && r.Host != "localhost:"+port {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(s.token)) != 1 {
			http.Error(rw, "This page's address is missing its key. Open Stockroom Setup again.", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(rw, r)
	})
}

type stateJSON struct {
	Platform  string   `json:"platform"`
	Steps     []string `json:"steps"`
	Notes     []string `json:"notes"`
	AskAdmin  bool     `json:"askAdmin"`
	AdminNote string   `json:"adminNote,omitempty"`
	Camera    bool     `json:"camera"`
	Need      *Need    `json:"need,omitempty"`

	Phase   string   `json:"phase"`
	Step    int      `json:"step"`
	From    int      `json:"from"`
	Lines   []string `json:"lines"`
	Error   string   `json:"error,omitempty"`
	URL     string   `json:"url,omitempty"`
	Message string   `json:"message,omitempty"`
}

// handleState answers everything the page shows. ?from=N asks for the log
// from line N on, so a poll carries only what is new.
func (s *session) handleState(rw http.ResponseWriter, r *http.Request) {
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	var need *Need
	s.mu.Lock()
	asking := s.phase == phaseAsk
	s.mu.Unlock()
	if asking && s.w.Need != nil {
		need = s.w.Need()
	}

	s.mu.Lock()
	s.lastSeen = time.Now()
	if from < 0 || from > len(s.lines) {
		from = len(s.lines)
	}
	st := stateJSON{
		Platform: s.w.Platform, Steps: s.w.Steps, Notes: s.w.Notes,
		AskAdmin: s.w.AskAdmin, AdminNote: s.w.AdminNote, Camera: s.w.Camera, Need: need,
		Phase: s.phase, Step: s.step, From: from,
		Lines: append([]string{}, s.lines[from:]...),
		Error: s.err, URL: s.result.URL, Message: s.result.Message,
	}
	s.mu.Unlock()
	writeJSON(rw, http.StatusOK, st)
}

func (s *session) handleRun(runCtx context.Context, rw http.ResponseWriter, r *http.Request) {
	var a Answers
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<16)).Decode(&a); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]string{"error": "the page sent something unreadable"})
		return
	}
	a.AdminNumber = strings.TrimSpace(a.AdminNumber)
	if !s.w.AskAdmin {
		a.AdminNumber, a.AdminPassword = "", ""
	}
	if !s.w.Camera {
		a.WithCamera = false
	}
	if s.w.Need != nil {
		if need := s.w.Need(); need != nil {
			writeJSON(rw, http.StatusConflict, map[string]string{"error": need.Message})
			return
		}
	}
	if s.w.Check != nil {
		if err := s.w.Check(a); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}

	s.mu.Lock()
	// A failed install may be tried again: setup repairs rather than
	// reinstalls. A finished or running one may not.
	if s.phase != phaseAsk && s.phase != phaseFailed {
		s.mu.Unlock()
		writeJSON(rw, http.StatusConflict, map[string]string{"error": "the install has already started"})
		return
	}
	s.phase, s.lines, s.partial, s.step, s.err = phaseRunning, nil, "", -1, ""
	s.lastSeen = time.Now()
	s.mu.Unlock()

	go func() {
		res, err := s.w.Run(runCtx, a, (*logWriter)(s))
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.partial != "" {
			s.addLine(s.partial)
			s.partial = ""
		}
		// The page is told only now, so give it a full Idle to notice.
		s.lastSeen = time.Now()
		if err != nil {
			s.phase, s.err = phaseFailed, err.Error()
			return
		}
		s.phase, s.result = phaseDone, res
	}()
	rw.WriteHeader(http.StatusAccepted)
}

// handleBye is the page being closed or reloaded. It leaves ten seconds for
// a reload to ask again, then the wizard exits rather than wait out Idle.
func (s *session) handleBye(rw http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if soon := time.Now().Add(10*time.Second - s.idle); soon.Before(s.lastSeen) {
		s.lastSeen = soon
	}
	s.mu.Unlock()
	rw.WriteHeader(http.StatusNoContent)
}

// addLine records one line of output. The caller holds s.mu.
func (s *session) addLine(line string) {
	line = strings.TrimRight(line, "\r")
	if name, ok := stepHeading(line); ok {
		for i, step := range s.w.Steps {
			if step == name {
				s.step = i
			}
		}
	}
	s.lines = append(s.lines, line)
}

// stepHeading reads the name out of "== Name ==".
func stepHeading(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if len(line) < 7 || !strings.HasPrefix(line, "== ") || !strings.HasSuffix(line, " ==") {
		return "", false
	}
	return line[3 : len(line)-3], true
}

// logWriter is the session as Run's output: it splits what arrives into lines.
type logWriter session

func (l *logWriter) Write(p []byte) (int, error) {
	s := (*session)(l)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != phaseRunning {
		return 0, errors.New("the install is over")
	}
	text := s.partial + string(p)
	for {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			break
		}
		s.addLine(text[:i])
		text = text[i+1:]
	}
	s.partial = text
	return len(p), nil
}

func writeJSON(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(v)
}
