package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is the Env's Out while GUI serves on another goroutine.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

var guiURL = regexp.MustCompile(`(?m)^(http://127\.0\.0\.1:\d+)/\?(t=[0-9a-f]{32})$`)

// guiPage is the browser's side of one graphical setup.
type guiPage struct {
	t     *testing.T
	base  string
	query string
	done  chan error
}

type guiState struct {
	Platform  string   `json:"platform"`
	Steps     []string `json:"steps"`
	Notes     []string `json:"notes"`
	AskAdmin  bool     `json:"askAdmin"`
	AdminNote string   `json:"adminNote"`
	Camera    bool     `json:"camera"`
	Need      *struct {
		Message string `json:"message"`
	} `json:"need"`
	Phase string   `json:"phase"`
	Lines []string `json:"lines"`
	Error string   `json:"error"`
	URL   string   `json:"url"`
}

func startGUI(t *testing.T, env *Env, opts Options) *guiPage {
	t.Helper()
	out := &lockedBuffer{}
	env.Out = out
	opts.NoOpen = true
	ctx, cancel := context.WithCancel(context.Background())
	p := &guiPage{t: t, done: make(chan error, 1)}
	go func() { p.done <- GUI(ctx, env, opts) }()
	t.Cleanup(func() {
		cancel()
		<-p.done
	})
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if m := guiURL.FindStringSubmatch(out.String()); m != nil {
			p.base, p.query = m[1], m[2]
			return p
		}
		select {
		case err := <-p.done:
			p.done <- err
			t.Fatalf("GUI returned before serving: %v", err)
		default:
		}
	}
	t.Fatalf("GUI never printed its address:\n%s", out.String())
	return nil
}

func (p *guiPage) state() guiState {
	p.t.Helper()
	res, err := http.Get(p.base + "/api/state?" + p.query)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	var st guiState
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		p.t.Fatal(err)
	}
	return st
}

func (p *guiPage) install(body string) int {
	p.t.Helper()
	res, err := http.Post(p.base+"/api/run?"+p.query, "application/json", strings.NewReader(body))
	if err != nil {
		p.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func (p *guiPage) wait() guiState {
	p.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if st := p.state(); st.Phase == "done" || st.Phase == "failed" {
			return st
		}
	}
	p.t.Fatal("the install never ended")
	return guiState{}
}

// TestGUIInstallsWhatThePageAsked: the page's answers reach the config the
// same way the flags do, and the page ends on the address Stockroom answers.
func TestGUIInstallsWhatThePageAsked(t *testing.T) {
	env, run, _ := testEnv(t)
	p := startGUI(t, env, Options{})

	st := p.state()
	if st.Platform != "Linux" || !st.AskAdmin || !st.Camera || strings.Join(st.Steps, ",") != strings.Join(stepNames, ",") {
		t.Fatalf("first state = %+v", st)
	}
	if !strings.Contains(strings.Join(st.Notes, "\n"), env.Paths.ConfigFile) {
		t.Errorf("the notes don't say where the config goes: %q", st.Notes)
	}

	// The failsafe's rules are checked before anything is installed.
	if code := p.install(`{"adminNumber":"900100","adminPassword":"short"}`); code != http.StatusBadRequest {
		t.Fatalf("a short password got %d, want 400", code)
	}
	if len(run.cmds) != 0 {
		t.Fatalf("a refused answer still ran %v", run.cmds)
	}

	if code := p.install(`{"adminNumber":"900100","adminPassword":"password123"}`); code != http.StatusAccepted {
		t.Fatalf("install got %d", code)
	}
	end := p.wait()
	if end.Phase != "done" || end.URL != "http://127.0.0.1:8080" {
		t.Fatalf("end state = %+v\n%s", end, strings.Join(end.Lines, "\n"))
	}
	cfg, err := os.ReadFile(env.Paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ADMIN_STUDENT_NUMBER='900100'", "ADMIN_PASSWORD='password123'"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("config lacks %s", want)
		}
	}
	log := strings.Join(end.Lines, "\n")
	if strings.Contains(log, "password123") {
		t.Error("the failsafe admin's password is in the page's log")
	}
	for _, name := range stepNames {
		if !strings.Contains(log, "== "+name+" ==") {
			t.Errorf("the log never announced the %s step", name)
		}
	}
	if !run.ran("systemctl restart stockroom") {
		t.Error("the service was never started")
	}
	if run.ran("camera.sh") {
		t.Error("the camera started without being asked for")
	}
}

// TestGUIKeepsAnExistingConfig: on a machine that is already set up the page
// has no failsafe admin to ask for, and ignores one sent anyway.
func TestGUIKeepsAnExistingConfig(t *testing.T) {
	env, _, out := testEnv(t)
	if err := Setup(context.Background(), env, nonInteractive()); err != nil {
		t.Fatalf("Setup: %v\n%s", err, out)
	}
	before, _ := os.ReadFile(env.Paths.ConfigFile)

	p := startGUI(t, env, Options{})
	st := p.state()
	if st.AskAdmin || !strings.Contains(st.AdminNote, env.Paths.ConfigFile) {
		t.Fatalf("state = %+v", st)
	}
	if code := p.install(`{"adminNumber":"900100","adminPassword":"password123"}`); code != http.StatusAccepted {
		t.Fatalf("install got %d", code)
	}
	if end := p.wait(); end.Phase != "done" {
		t.Fatalf("end state = %+v", end)
	}
	if after, _ := os.ReadFile(env.Paths.ConfigFile); !bytes.Equal(before, after) {
		t.Error("the repair rewrote the config")
	}
}

// TestGUIShowsWhereSetupStopped: a step's error reaches the page whole.
func TestGUIShowsWhereSetupStopped(t *testing.T) {
	env, run, _ := testEnv(t)
	run.listen = "*"
	p := startGUI(t, env, Options{})
	p.install(`{}`)
	end := p.wait()
	if end.Phase != "failed" || !strings.Contains(end.Error, "setup stopped at postgresql") || !strings.Contains(end.Error, "listen_addresses = 'localhost'") {
		t.Fatalf("end state = %+v", end)
	}
}

func TestGUINeedsRootOnLinux(t *testing.T) {
	env, _, _ := testEnv(t)
	env.Root = false
	err := GUI(context.Background(), env, Options{NoOpen: true})
	if err == nil || !strings.Contains(err.Error(), "sudo stockroom setup --gui") {
		t.Fatalf("GUI without root = %v", err)
	}
}

// macEnv is a Mac with Homebrew rooted in a temp directory.
func macEnv(t *testing.T) (*Env, *fakeRunner, string) {
	t.Helper()
	env, run, _ := testEnv(t)
	prefix := filepath.Join(t.TempDir(), "homebrew")
	env.GOOS, env.Root, env.SudoUser, env.Systemd = "darwin", false, "", false
	env.Paths.BrewPrefix = prefix
	env.Paths.Binary = "/Volumes/Stockroom/Install Stockroom.app/Contents/Resources/stockroom-arm64"
	return env, run, prefix
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestGUIAsksForHomebrewFirst: the installer app on a Mac with no Homebrew
// says so, with the link, rather than failing halfway.
func TestGUIAsksForHomebrewFirst(t *testing.T) {
	env, _, prefix := macEnv(t)
	p := startGUI(t, env, Options{InstallPackages: true})
	st := p.state()
	if st.Need == nil || !strings.Contains(st.Need.Message, "Homebrew") || st.Steps[0] != packagesStep {
		t.Fatalf("state = %+v", st)
	}
	if code := p.install(`{}`); code != http.StatusConflict {
		t.Errorf("install without Homebrew got %d, want 409", code)
	}
	touch(t, filepath.Join(prefix, "bin", "brew"))
	if st := p.state(); st.Need != nil {
		t.Errorf("the need stayed after Homebrew arrived: %+v", st.Need)
	}
}

// TestInstallPackages: the cask is installed only when something is missing,
// and the service is pointed at the binary Homebrew linked, never at the copy
// inside the installer app.
func TestInstallPackages(t *testing.T) {
	env, run, prefix := macEnv(t)
	env.Out = &bytes.Buffer{}
	app := env.Paths.Binary
	linked := filepath.Join(prefix, "bin", "stockroom")

	err := installPackages(context.Background(), env)
	if err == nil || !strings.Contains(err.Error(), linked) {
		t.Fatalf("with a brew that installs nothing: %v", err)
	}
	if !run.ran("brew install --cask " + caskName) {
		t.Fatalf("brew was never asked for the cask: %v", run.cmds)
	}
	if env.Paths.Binary != app {
		t.Error("the binary moved although the install failed")
	}

	for _, p := range []string{linked, filepath.Join(prefix, "opt", brewFormula, "bin", "postgres"), filepath.Join(prefix, "bin", "rclone")} {
		touch(t, p)
	}
	run.cmds = nil
	if err := installPackages(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if len(run.cmds) != 0 {
		t.Errorf("everything was installed, and brew ran anyway: %v", run.cmds)
	}
	if env.Paths.Binary != linked {
		t.Errorf("Binary = %s, want %s", env.Paths.Binary, linked)
	}
}
