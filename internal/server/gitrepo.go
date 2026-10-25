// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// gitRepo commits published content to the site's git repository (and
// pushes it), so git stays the source of truth for posts.
type gitRepo struct {
	dir    string
	branch string
	push   bool
	log    *log.Logger
	name   string
	email  string
	mu     sync.Mutex
}

// ErrNotRepo means the site folder is not a git repository; content is
// written but not committed.
var ErrNotRepo = errors.New("the site folder is not a git repository")

func (g *gitRepo) run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", g.dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME="+g.name, "GIT_AUTHOR_EMAIL="+g.email,
		"GIT_COMMITTER_NAME="+g.name, "GIT_COMMITTER_EMAIL="+g.email,
	)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (g *gitRepo) isRepo() bool {
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	_, err := g.run("rev-parse", "--is-inside-work-tree")
	return err == nil
}

func (g *gitRepo) hasRemote() bool {
	out, err := g.run("remote")
	return err == nil && strings.TrimSpace(out) != ""
}

// Pull fast-forwards to the remote branch. Local publish commits are never
// rewritten; a diverged branch is an error for the owner to resolve.
func (g *gitRepo) Pull() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pullLocked()
}

func (g *gitRepo) pullLocked() error {
	if !g.isRepo() || !g.hasRemote() {
		return nil
	}
	if _, err := g.run("fetch", "--quiet", "origin", g.branch); err != nil {
		return err
	}
	if _, err := g.run("merge", "--ff-only", "--quiet", "origin/"+g.branch); err != nil {
		// Our own unpushed commits may be ahead; rebase them onto the remote.
		if _, err2 := g.run("rebase", "--quiet", "origin/"+g.branch); err2 != nil {
			_, _ = g.run("rebase", "--abort")
			return fmt.Errorf("the site repository has diverged from origin/%s; resolve it by hand: %w", g.branch, err)
		}
	}
	return nil
}

// Commit stages paths (relative to the repo; deletions included) and
// commits them. It returns the new commit, or "" when nothing changed.
func (g *gitRepo) Commit(paths []string, message string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.isRepo() {
		return "", ErrNotRepo
	}
	args := append([]string{"add", "--all", "--"}, paths...)
	if _, err := g.run(args...); err != nil {
		return "", err
	}
	if out, _ := g.run("diff", "--cached", "--name-only"); strings.TrimSpace(out) == "" {
		return "", nil
	}
	if _, err := g.run("commit", "--quiet", "-m", message); err != nil {
		return "", err
	}
	sha, err := g.run("rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	if g.push && g.hasRemote() {
		if err := g.pushLocked(); err != nil {
			return sha, err
		}
	}
	return sha, nil
}

func (g *gitRepo) pushLocked() error {
	if _, err := g.run("push", "--quiet", "origin", "HEAD:"+g.branch); err == nil {
		return nil
	}
	// Someone pushed meanwhile: rebase onto it and try once more.
	if err := g.pullLocked(); err != nil {
		return err
	}
	_, err := g.run("push", "--quiet", "origin", "HEAD:"+g.branch)
	return err
}

// Head returns the current commit (short) or "".
func (g *gitRepo) Head() string {
	sha, err := g.run("rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return sha
}

// rel makes a path relative to the repository.
func (g *gitRepo) rel(p string) string {
	r, err := filepath.Rel(g.dir, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}
