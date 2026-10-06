package wizard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a Wizard's Log that the test reads while Serve writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

var pageURL = regexp.MustCompile(`(?m)^http://127\.0\.0\.1:\d+/\?t=[0-9a-f]{32}$`)

// serve starts w and returns its address, split at the token, and a channel
// that gets Serve's result.
func serve(t *testing.T, w *Wizard) (base, token string, done chan error) {
	t.Helper()
	log := &syncBuffer{}
	w.Log = log
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done = make(chan error, 1)
	go func() { done <- w.Serve(ctx) }()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if u := pageURL.FindString(log.String()); u != "" {
			base, token, _ = strings.Cut(u, "/?t=")
			return base, token, done
		}
	}
	t.Fatalf("Serve never printed its address:\n%s", log.String())
	return "", "", nil
}

func getState(t *testing.T, base, token string) stateJSON {
	t.Helper()
	res, err := http.Get(base + "/api/state?t=" + token)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/api/state answered %s", res.Status)
	}
	var st stateJSON
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func post(t *testing.T, url, body string) (int, string) {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

func waitPhase(t *testing.T, base, token, phase string) stateJSON {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if st := getState(t, base, token); st.Phase == phase {
			return st
		}
	}
	t.Fatalf("never reached phase %q", phase)
	return stateJSON{}
}

// TestInstallRunsAndReportsItsSteps walks the page's whole conversation: the
// state it draws from, a refused answer, an install, and the end.
func TestInstallRunsAndReportsItsSteps(t *testing.T) {
	var got Answers
	release := make(chan struct{})
	w := &Wizard{
		Platform: "Linux", Steps: []string{"One", "Two"}, Notes: []string{"note"},
		AskAdmin: true, Camera: true,
		Check: func(a Answers) error {
			if a.AdminNumber == "bad" {
				return errors.New("digits only")
			}
			return nil
		},
		Run: func(ctx context.Context, a Answers, out io.Writer) (Result, error) {
			got = a
			fmt.Fprint(out, "== One ==\n  [OK] first\n== Tw")
			fmt.Fprint(out, "o ==\n")
			<-release
			fmt.Fprint(out, "  [OK] second")
			return Result{URL: "http://127.0.0.1:8080", Message: "ready"}, nil
		},
	}
	base, token, done := serve(t, w)

	st := getState(t, base, token)
	if st.Phase != phaseAsk || st.Platform != "Linux" || !st.AskAdmin || !st.Camera || len(st.Steps) != 2 || st.Step != -1 {
		t.Fatalf("first state = %+v", st)
	}

	if code, body := post(t, base+"/api/run?t="+token, `{"adminNumber":"bad","adminPassword":"password123"}`); code != http.StatusBadRequest || !strings.Contains(body, "digits only") {
		t.Fatalf("a refused answer got %d %s", code, body)
	}
	if st := getState(t, base, token); st.Phase != phaseAsk {
		t.Fatalf("a refused answer moved the phase to %s", st.Phase)
	}

	if code, body := post(t, base+"/api/run?t="+token, `{"adminNumber":" 900100 ","adminPassword":"password123","withCamera":true}`); code != http.StatusAccepted {
		t.Fatalf("run got %d %s", code, body)
	}
	if code, _ := post(t, base+"/api/run?t="+token, `{}`); code != http.StatusConflict {
		t.Errorf("a second run while one is going got %d, want 409", code)
	}
	var running stateJSON
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if running = getState(t, base, token); running.Step == 1 {
			break
		}
	}
	if running.Phase != phaseRunning || running.Step != 1 || strings.Join(running.Lines, "|") != "== One ==|  [OK] first|== Two ==" {
		t.Fatalf("running state = %+v", running)
	}
	close(release)

	end := waitPhase(t, base, token, phaseDone)
	if end.URL != "http://127.0.0.1:8080" || end.Message != "ready" || end.Lines[len(end.Lines)-1] != "  [OK] second" {
		t.Errorf("end state = %+v", end)
	}
	if got.AdminNumber != "900100" || got.AdminPassword != "password123" || !got.WithCamera {
		t.Errorf("Run got %+v", got)
	}
	// ?from= carries only what the page hasn't shown.
	res, err := http.Get(base + "/api/state?from=3&t=" + token)
	if err != nil {
		t.Fatal(err)
	}
	var tail stateJSON
	json.NewDecoder(res.Body).Decode(&tail)
	res.Body.Close()
	if len(tail.Lines) != 1 || tail.From != 3 {
		t.Errorf("from=3 gave %+v", tail)
	}

	if code, _ := post(t, base+"/api/quit?t="+token, ""); code != http.StatusNoContent {
		t.Errorf("quit got %d", code)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after quit")
	}
}

// TestAFailedInstallCanBeTriedAgain: setup repairs, so the page offers it.
func TestAFailedInstallCanBeTriedAgain(t *testing.T) {
	runs := 0
	w := &Wizard{
		Platform: "macOS", Steps: []string{"One"},
		Run: func(ctx context.Context, a Answers, out io.Writer) (Result, error) {
			runs++
			fmt.Fprintf(out, "== One ==\nrun %d\n", runs)
			if runs == 1 {
				return Result{}, errors.New("postgres is not installed")
			}
			return Result{Message: "restart"}, nil
		},
	}
	base, token, _ := serve(t, w)
	post(t, base+"/api/run?t="+token, `{"adminNumber":"900100","adminPassword":"password123"}`)
	st := waitPhase(t, base, token, phaseFailed)
	if st.Error != "postgres is not installed" || st.Step != 0 {
		t.Fatalf("failed state = %+v", st)
	}
	if code, body := post(t, base+"/api/run?t="+token, `{}`); code != http.StatusAccepted {
		t.Fatalf("the retry got %d %s", code, body)
	}
	st = waitPhase(t, base, token, phaseDone)
	if strings.Join(st.Lines, "|") != "== One ==|run 2" || st.URL != "" || st.Message != "restart" {
		t.Errorf("after the retry = %+v", st)
	}
}

// TestOnlyThisPageReachesTheWizard: no token, no answer, and a request that
// names another host is refused even with one.
func TestOnlyThisPageReachesTheWizard(t *testing.T) {
	ran := false
	w := &Wizard{Platform: "Linux", Run: func(context.Context, Answers, io.Writer) (Result, error) {
		ran = true
		return Result{}, nil
	}}
	base, token, _ := serve(t, w)

	for _, url := range []string{base + "/", base + "/api/state", base + "/api/state?t=" + strings.Repeat("0", 32)} {
		res, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s = %d, want 403", url, res.StatusCode)
		}
	}
	if code, _ := post(t, base+"/api/run", `{}`); code != http.StatusForbidden {
		t.Errorf("run without the token = %d, want 403", code)
	}

	req, _ := http.NewRequest(http.MethodPost, base+"/api/run?t="+token, strings.NewReader(`{}`))
	req.Host = "evil.example"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("a rebinding Host got %d, want 403", res.StatusCode)
	}
	if ran {
		t.Error("an install started from a refused request")
	}

	res, err = http.Get(base + "/?t=" + token)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("<title>Stockroom Setup</title>")) {
		t.Errorf("the page itself = %d", res.StatusCode)
	}
}

// TestNeedBlocksTheInstall: a missing prerequisite shows on the page and
// refuses Install until it is there.
func TestNeedBlocksTheInstall(t *testing.T) {
	missing := true
	w := &Wizard{
		Platform: "macOS",
		Need: func() *Need {
			if missing {
				return &Need{Message: "no Homebrew", LinkURL: "https://brew.sh"}
			}
			return nil
		},
		Run: func(context.Context, Answers, io.Writer) (Result, error) { return Result{}, nil },
	}
	base, token, _ := serve(t, w)
	if st := getState(t, base, token); st.Need == nil || st.Need.Message != "no Homebrew" {
		t.Fatalf("state = %+v", st)
	}
	if code, body := post(t, base+"/api/run?t="+token, `{}`); code != http.StatusConflict || !strings.Contains(body, "no Homebrew") {
		t.Errorf("run with a need = %d %s", code, body)
	}
	missing = false
	if st := getState(t, base, token); st.Need != nil {
		t.Errorf("the need stayed: %+v", st.Need)
	}
	if code, _ := post(t, base+"/api/run?t="+token, `{}`); code != http.StatusAccepted {
		t.Errorf("run = %d", code)
	}
}

// TestAnAbandonedWizardExits: a closed tab must not leave a root process
// listening for ever, and a running install must not be cut short by one.
func TestAnAbandonedWizardExits(t *testing.T) {
	release := make(chan struct{})
	w := &Wizard{
		Platform: "Linux", Idle: 50 * time.Millisecond,
		Run: func(context.Context, Answers, io.Writer) (Result, error) {
			<-release
			return Result{}, nil
		},
	}
	base, token, done := serve(t, w)
	post(t, base+"/api/run?t="+token, `{}`)
	select {
	case <-done:
		t.Fatal("Serve returned while an install was running")
	case <-time.After(1500 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return once the page was gone")
	}
}

// TestPageColoursComeFromTheTokens holds the page to the app's one palette.
// tokens.css is the only file allowed to define a colour (CLAUDE.md §8); the
// page can't import it, so every hex value it carries must be one of that
// file's.
func TestPageColoursComeFromTheTokens(t *testing.T) {
	tokens, err := os.ReadFile("../../packages/ui/src/lib/styles/tokens.css")
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(--[a-z-]+):\s*(#[0-9a-fA-F]{3,8})\b`)
	defined := map[string]string{}
	for _, m := range decl.FindAllStringSubmatch(string(tokens), -1) {
		defined[m[1]] = strings.ToLower(m[2])
	}
	found := 0
	for _, m := range decl.FindAllStringSubmatch(string(page), -1) {
		found++
		if defined[m[1]] != strings.ToLower(m[2]) {
			t.Errorf("page.html sets %s to %s, tokens.css has %q", m[1], m[2], defined[m[1]])
		}
	}
	if found == 0 {
		t.Fatal("found no colours in page.html; has the :root block moved?")
	}
	stripped := decl.ReplaceAllString(string(page), "")
	if stray := regexp.MustCompile(`#[0-9a-fA-F]{6}\b|#[0-9a-fA-F]{3}\b|rgba?\(`).FindString(stripped); stray != "" {
		t.Errorf("page.html has a colour outside its :root block: %s", stray)
	}
}
