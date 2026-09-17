package stockroom

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// GitHub, over the REST API with no git binary (docs/design/backup.md §E.4).
//
// One secret, nothing to install, no credential helper and no SSH key. The
// same token serves the push and the browse-history feature, and a school
// firewall that blocks Google Drive rarely blocks github.com, which is the
// whole reason for having a second target at all.
//
// Layout (§D.3): one fixed backup/ path, overwritten nightly, one commit per
// run. CSVs are text, so git stores night-to-night deltas of a few KB; dated
// zips would add a full incompressible blob every night with no way to prune
// without rewriting history. The commit log *is* the dated backup list the
// picker reads.

const (
	githubAPI     = "https://api.github.com"
	githubDir     = "backup/"
	githubTimeout = 5 * time.Minute
	// githubMaxCommits bounds the version list. A hundred nights is more
	// history than the retention window keeps anyway.
	githubMaxCommits = 100
)

type githubTarget struct {
	repo     string
	token    string
	keepDays int
	baseURL  string // overridden by tests
	client   *http.Client
}

func newGitHubTarget(s Settings) *githubTarget {
	return &githubTarget{
		repo:     strings.Trim(s.GitHubRepo, "/"),
		token:    s.GitHubToken,
		keepDays: s.KeepDays,
		baseURL:  githubAPI,
		client:   &http.Client{Timeout: githubTimeout},
	}
}

func (g *githubTarget) Name() string { return githubTargetName }

/* ----------------------------------------------------------------- api ---- */

// do makes one API call. Every GitHub error comes back naming the status and
// the message GitHub sent, because "push failed" on a backup screen at 8 a.m.
// is not something anybody can act on: "401 Bad credentials" is.
func (g *githubTarget) do(ctx context.Context, method, urlPath string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("github: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	url := urlPath
	if !strings.HasPrefix(url, "http") {
		url = g.baseURL + urlPath
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: github %s %s", ErrNotFound, method, urlPath)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github %s: %s", resp.Status, githubMessage(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("github: unreadable response to %s %s: %w", method, urlPath, err)
		}
	}
	return nil
}

func githubMessage(raw []byte) string {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &body); err == nil && body.Message != "" {
		return body.Message
	}
	return oneLine(string(raw))
}

/* ---------------------------------------------------------------- push ---- */

// Push writes one commit holding the whole backup, atomically.
//
// The Git Data API is the only way to commit several files at once: blobs,
// then a tree, then a commit, then one reference update. Anything built on the
// contents API would write a commit per file, and a run interrupted halfway
// would leave a backup/ folder holding half of tonight and half of last night
// -- which passes every check a restore makes and is not a backup of anything.
func (g *githubTarget) Push(ctx context.Context, req pushRequest) (string, error) {
	files, err := githubFiles(req)
	if err != nil {
		return "", err
	}

	branch, err := g.defaultBranch(ctx)
	if err != nil {
		return "", err
	}
	parent, hasParent, err := g.headCommit(ctx, branch)
	if err != nil {
		return "", err
	}

	entries := make([]githubTreeEntry, 0, len(files)+1)
	for _, f := range files {
		sha, err := g.createBlob(ctx, f.content)
		if err != nil {
			return "", err
		}
		entries = append(entries, githubTreeEntry{Path: f.path, Mode: "100644", Type: "blob", SHA: sha})
	}
	readme, err := g.createBlob(ctx, repoReadme(req))
	if err != nil {
		return "", err
	}
	entries = append(entries, githubTreeEntry{Path: "README.md", Mode: "100644", Type: "blob", SHA: readme})

	// No base_tree: the tree is built from scratch, so a file that stopped
	// being part of a backup (a table dropped by a migration) disappears
	// instead of lingering forever as a row nothing writes. This repository
	// holds nothing but the backup, which is what makes that safe.
	var tree struct {
		SHA string `json:"sha"`
	}
	if err := g.do(ctx, http.MethodPost, "/repos/"+g.repo+"/git/trees",
		map[string]any{"tree": entries}, &tree); err != nil {
		return "", err
	}

	parents := []string{}
	if hasParent {
		parents = append(parents, parent)
	}
	commitSHA, err := g.createCommit(ctx, commitMessage(req), tree.SHA, parents)
	if err != nil {
		return "", err
	}
	if err := g.updateRef(ctx, branch, commitSHA, hasParent, false); err != nil {
		return "", err
	}

	// Retention runs after a successful push, never before: pruning history
	// and then failing to write tonight's backup would trade a full history
	// for nothing.
	if err := g.pruneHistory(ctx, branch, tree.SHA, commitSHA, req); err != nil {
		// A purge that failed is not a backup that failed. The commit is
		// there; say so and report the purge separately in the log.
		return commitSHA, fmt.Errorf("the backup was pushed, but trimming the old history failed: %w", err)
	}
	return commitSHA, nil
}

type githubFile struct {
	path    string
	content []byte
}

// githubFiles is what one run publishes.
//
// Unencrypted, the archive is unpacked so the repository holds readable CSVs
// that git can delta -- the whole justification for the §D.3 layout. Encrypted
// (§C.5), it publishes the sealed archive and nothing else: unpacking it would
// push the plaintext the encryption exists to prevent leaving the machine.
func githubFiles(req pushRequest) ([]githubFile, error) {
	raw, err := os.ReadFile(req.ArchivePath)
	if err != nil {
		return nil, fmt.Errorf("read the archive to push: %w", err)
	}
	if req.Encrypted {
		return []githubFile{{path: githubDir + path.Base(req.ArchivePath), content: raw}}, nil
	}

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("read the archive to push: %w", err)
	}
	files := make([]githubFile, 0, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("read %s from the archive: %w", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s from the archive: %w", f.Name, err)
		}
		files = append(files, githubFile{path: githubDir + f.Name, content: content})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, nil
}

type githubTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

func (g *githubTarget) createBlob(ctx context.Context, content []byte) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	err := g.do(ctx, http.MethodPost, "/repos/"+g.repo+"/git/blobs", map[string]any{
		"content":  base64.StdEncoding.EncodeToString(content),
		"encoding": "base64",
	}, &out)
	return out.SHA, err
}

func (g *githubTarget) createCommit(ctx context.Context, message, tree string, parents []string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	err := g.do(ctx, http.MethodPost, "/repos/"+g.repo+"/git/commits", map[string]any{
		"message": message,
		"tree":    tree,
		"parents": parents,
	}, &out)
	return out.SHA, err
}

func (g *githubTarget) updateRef(ctx context.Context, branch, sha string, exists, force bool) error {
	if !exists {
		return g.do(ctx, http.MethodPost, "/repos/"+g.repo+"/git/refs", map[string]any{
			"ref": "refs/heads/" + branch,
			"sha": sha,
		}, nil)
	}
	return g.do(ctx, http.MethodPatch, "/repos/"+g.repo+"/git/refs/heads/"+branch, map[string]any{
		"sha":   sha,
		"force": force,
	}, nil)
}

func (g *githubTarget) defaultBranch(ctx context.Context) (string, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.do(ctx, http.MethodGet, "/repos/"+g.repo, nil, &repo); err != nil {
		return "", err
	}
	if repo.DefaultBranch == "" {
		return "main", nil
	}
	return repo.DefaultBranch, nil
}

// headCommit is the branch tip, or exists = false for a repository with no
// commits yet -- which is what a freshly created private repo looks like, and
// is the first thing this code ever sees.
func (g *githubTarget) headCommit(ctx context.Context, branch string) (string, bool, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := g.do(ctx, http.MethodGet, "/repos/"+g.repo+"/git/ref/heads/"+branch, nil, &ref)
	if err != nil {
		if strings.Contains(err.Error(), ErrNotFound.Error()) {
			return "", false, nil
		}
		return "", false, err
	}
	return ref.Object.SHA, ref.Object.SHA != "", nil
}

func commitMessage(req pushRequest) string {
	assets := req.Manifest.Rows["assets"]
	accounts := req.Manifest.Rows["profiles"]
	categories := req.Manifest.Rows["categories"]
	return fmt.Sprintf("Backup %s %s — %d assets, %d accounts, %d categories",
		req.Day, req.Manifest.RanAt.Format("15:04"), assets, accounts, categories)
}

/* ----------------------------------------------------------- retention ---- */

// pruneHistory bounds the repository's history at keep_days by rewriting the
// branch onto a fresh orphan root.
//
// Overwriting backup/accounts.csv does not remove last month's copy -- it adds
// a commit, and every prior version stays reachable forever. That file is a
// credential roster (§B.1), so a student who leaves and is deleted from the
// database is otherwise still a working scan-login in every commit made before
// the deletion. "Deleted" that does not propagate is not deleted.
//
// Two caveats, both documented in docs/BACKUP-SETUP.md because they belong in
// the operator's head:
//
//   - Unreachable is not erased. GitHub garbage-collects on its own schedule,
//     and until it does the old objects are fetchable by anyone who already
//     knows a sha. Deleting and recreating the repository is the only way to be
//     certain, and that is the documented procedure for scrubbing one account.
//   - A force-update is the one irreversible write this application makes.
//     It is bounded by a setting, logged like a backup run, and triggered by
//     nothing but the retention clock.
func (g *githubTarget) pruneHistory(ctx context.Context, branch, tree, head string, req pushRequest) error {
	if g.keepDays < 1 {
		return nil
	}
	commits, err := g.commitList(ctx)
	if err != nil {
		return err
	}
	if len(commits) < 2 {
		return nil
	}
	oldest := commits[len(commits)-1]
	cutoff := time.Now().AddDate(0, 0, -g.keepDays)
	if oldest.At.After(cutoff) {
		return nil
	}

	root, err := g.createCommit(ctx, commitMessage(req)+"\n\nHistory before "+cutoff.Format("2006-01-02")+
		" was removed: these files hold student numbers, which are login credentials.", tree, []string{})
	if err != nil {
		return err
	}
	return g.updateRef(ctx, branch, root, true, true)
}

/* ------------------------------------------------------------ versions ---- */

type githubCommit struct {
	SHA     string
	Message string
	At      time.Time
}

func (g *githubTarget) commitList(ctx context.Context) ([]githubCommit, error) {
	var raw []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message   string `json:"message"`
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	err := g.do(ctx, http.MethodGet,
		fmt.Sprintf("/repos/%s/commits?path=%s&per_page=%d", g.repo, strings.TrimSuffix(githubDir, "/"), githubMaxCommits),
		nil, &raw)
	if err != nil {
		// A repository with no commits answers 409 rather than an empty list.
		if strings.Contains(err.Error(), "409") || strings.Contains(err.Error(), ErrNotFound.Error()) {
			return []githubCommit{}, nil
		}
		return nil, err
	}
	out := make([]githubCommit, 0, len(raw))
	for _, c := range raw {
		out = append(out, githubCommit{SHA: c.SHA, Message: c.Commit.Message, At: c.Commit.Committer.Date})
	}
	return out, nil
}

// Versions is the commit log, which is the dated backup list.
func (g *githubTarget) Versions(ctx context.Context) ([]BackupVersion, error) {
	commits, err := g.commitList(ctx)
	if err != nil {
		return nil, fmt.Errorf("list GitHub backups: %w", err)
	}
	out := make([]BackupVersion, 0, len(commits))
	for _, c := range commits {
		out = append(out, BackupVersion{
			ID:    c.SHA,
			Label: c.At.Local().Format("2006-01-02 15:04"),
			At:    c.At,
			Note:  firstLineOf(c.Message),
		})
	}
	return out, nil
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

/* --------------------------------------------------------------- fetch ---- */

// Fetch rebuilds the archive from a commit.
//
// The repository stores the archive *unpacked*, so what comes back has to be
// repackaged into the same zip the local export writes. That is what makes
// upload, Drive-by-date and GitHub-by-date converge on one RestoreFromZip
// rather than three restores, only one of which is ever exercised.
func (g *githubTarget) Fetch(ctx context.Context, id string) (io.ReadCloser, error) {
	if id == "" || strings.ContainsAny(id, "/?#") {
		return nil, fmt.Errorf("%w: %q is not a commit on this repository", ErrInvalid, id)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		g.baseURL+"/repos/"+g.repo+"/tarball/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download from GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("download from GitHub: %s: %s", resp.Status, githubMessage(raw))
	}

	zipped, err := repackTarball(resp.Body)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(zipped)), nil
}

// repackTarball turns GitHub's tar.gz of the whole repository into the archive
// zip. Files under backup/ become archive members with the backup/ prefix
// stripped; everything else (the README) is dropped.
//
// An encrypted backup is a single sealed file under backup/, and it is handed
// back as-is: it already *is* the archive, and rewrapping it in a zip would
// produce something no restore could open.
func repackTarball(r io.Reader) ([]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("read the GitHub download: %w", err)
	}
	defer gz.Close()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	found := 0
	var sealed []byte
	var total int64

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read the GitHub download: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// GitHub prefixes every path with "<owner>-<repo>-<sha>/".
		name := hdr.Name
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		if !strings.HasPrefix(name, githubDir) {
			continue
		}
		inner := strings.TrimPrefix(name, githubDir)
		// Bounded like the upload path, and for the same reason. A gzip
		// stream declares nothing about how much it expands to, so an
		// unbounded ReadAll per member is a decompression bomb read straight
		// into memory. The cap is on the running total across every backup/
		// member, not per file: a thousand members just under a per-file
		// limit would otherwise add up to the same exhaustion.
		remaining := int64(maxRestoreBytes) - total
		content, err := io.ReadAll(io.LimitReader(tr, remaining+1))
		if err != nil {
			return nil, fmt.Errorf("read %s from the GitHub download: %w", inner, err)
		}
		if int64(len(content)) > remaining {
			return nil, fmt.Errorf("%w: that backup expands to more than %d MB, which is not a Stockroom backup", ErrInvalid, maxRestoreBytes>>20)
		}
		total += int64(len(content))
		if strings.HasSuffix(inner, encryptedSuffix) {
			sealed = content
			continue
		}
		w, err := zw.Create(inner)
		if err != nil {
			return nil, fmt.Errorf("rebuild the archive: %w", err)
		}
		if _, err := w.Write(content); err != nil {
			return nil, fmt.Errorf("rebuild the archive: %w", err)
		}
		found++
	}
	if sealed != nil {
		return sealed, nil
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("rebuild the archive: %w", err)
	}
	if found == 0 {
		return nil, fmt.Errorf("%w: that commit has no backup/ folder in it", ErrNotFound)
	}
	return buf.Bytes(), nil
}

/* ---------------------------------------------------------------- test ---- */

// Test says more than "it worked": a token that can read but not write is the
// commonest way to set this up wrong, and it would otherwise fail at 2 a.m.
// with nobody watching. The fine-grained token needs Contents: Read and write
// (§F.2 step 7), and this is what checks it.
func (g *githubTarget) Test(ctx context.Context) error {
	var repo struct {
		FullName    string `json:"full_name"`
		Private     bool   `json:"private"`
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if err := g.do(ctx, http.MethodGet, "/repos/"+g.repo, nil, &repo); err != nil {
		if strings.Contains(err.Error(), ErrNotFound.Error()) {
			return fmt.Errorf("%w: GitHub cannot find %s, or the token has no access to it. Check the repository name and that the token lists it under \"Only select repositories\"", ErrNotFound, g.repo)
		}
		return err
	}
	if !repo.Permissions.Push {
		return fmt.Errorf("%w: the token works but can only read %s. Give it Repository permissions → Contents → Read and write", ErrForbidden, g.repo)
	}
	if !repo.Private {
		return fmt.Errorf("%w: %s is a public repository. Backups hold student numbers, which are login credentials, so the repository must be private", ErrInvalid, g.repo)
	}
	return nil
}

/* -------------------------------------------------------------- readme ---- */

// repoReadme is written on every push, so what somebody opening the repository
// reads always matches what is actually in it.
func repoReadme(req pushRequest) []byte {
	assets := req.Manifest.Rows["assets"]
	accounts := req.Manifest.Rows["profiles"]
	body := `# Stockroom backup

Automatic nightly backup of the media department's equipment checkout system.
**Do not edit these files by hand.**

Last backup: ` + req.Manifest.RanAt.Format("2006-01-02 15:04") + fmt.Sprintf(" — %d items, %d accounts", assets, accounts) + `

## What's here
| File | What it is |
|---|---|
| ` + "`backup/inventory.csv`" + ` | Every item, readable in Excel |
| ` + "`backup/accounts.csv`" + `  | Every user account |
| ` + "`backup/tables/*.csv`" + `  | Raw database tables — used to restore |
| ` + "`backup/manifest.json`" + ` | Row counts + schema version, checked during restore |

## Older backups
This folder always holds the most recent night. Previous nights are in the commit
history: click **backup/** then the clock icon, or
[view all backups](../../commits/main/backup).

## How to restore
Open Stockroom → sign in as an admin → **Admin → Backup → Restore from GitHub**,
then pick a date. Nothing needs to be downloaded.

If Stockroom will not start, see ` + "`RESTORE.md`" + ` inside any backup zip.

## This repository must stay private
These files hold student numbers, and a student number signs its owner in to
Stockroom by scanning their ID card with no password.
`
	if req.Encrypted {
		body += "\n**This backup is encrypted.** The archive under `backup/` can only be\nopened with the passphrase set in Stockroom's admin panel. If that passphrase\nis lost, the backup cannot be recovered.\n"
	}
	return []byte(body)
}
