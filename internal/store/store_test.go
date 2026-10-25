// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationsAndReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.Version()
	if v < 1 {
		t.Fatalf("version %d", v)
	}
	s.Close()
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.Backup("manual"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "backups"))
	if len(entries) != 1 {
		t.Errorf("backups = %d", len(entries))
	}
}

func TestDraftRevisionsThrottled(t *testing.T) {
	s := open(t)
	now := time.Unix(1_800_000_000, 0)
	s.SetClock(func() time.Time { return now })
	d, err := s.CreateDraft("post", "", `{"title":"A"}`, "one")
	if err != nil {
		t.Fatal(err)
	}
	s.SaveDraft(d.ID, `{"title":"A"}`, "two", false) // within a minute: no revision
	now = now.Add(61 * time.Second)
	s.SaveDraft(d.ID, `{"title":"A"}`, "three", false) // a minute later: revision
	s.SaveDraft(d.ID, `{"title":"A"}`, "four", true)   // forced: revision
	revs, _ := s.Revisions(d.ID)
	if len(revs) != 3 || revs[0].Body != "four" || revs[2].Body != "one" {
		t.Fatalf("revisions = %+v", revs)
	}
	got, _ := s.Draft(d.ID)
	if got.Body != "four" {
		t.Errorf("body = %q", got.Body)
	}
	if err := s.DeleteDraft(d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Draft(d.ID); !errors.Is(err, ErrNotFound) {
		t.Error("draft not deleted")
	}
}

func TestJobsIdempotentAndBackoff(t *testing.T) {
	s := open(t)
	now := time.Unix(1_800_000_000, 0)
	s.SetClock(func() time.Time { return now })
	added, _ := s.Enqueue("send", "send:post-1", "{}", 0)
	again, _ := s.Enqueue("send", "send:post-1", "{}", 0)
	if !added || again {
		t.Fatal("duplicate key enqueued twice")
	}
	jobs, _ := s.Claim(10, 60)
	if len(jobs) != 1 {
		t.Fatalf("claimed %d", len(jobs))
	}
	if more, _ := s.Claim(10, 60); len(more) != 0 {
		t.Fatal("locked job claimed twice")
	}
	s.Retry(jobs[0], errors.New("boom"))
	if more, _ := s.Claim(10, 60); len(more) != 0 {
		t.Fatal("retried job ran before its backoff")
	}
	now = now.Add(2 * time.Minute)
	jobs, _ = s.Claim(10, 60)
	if len(jobs) != 1 || jobs[0].Attempts != 2 || jobs[0].LastError != "boom" {
		t.Fatalf("after backoff: %+v", jobs)
	}
	s.Finish(jobs[0].ID, "ok")
	j, _ := s.JobByKey("send:post-1")
	if j.DoneAt == 0 || j.Result != "ok" {
		t.Errorf("job = %+v", j)
	}
}

func TestJobFailsAfterMaxAttempts(t *testing.T) {
	s := open(t)
	now := time.Unix(1_800_000_000, 0)
	s.SetClock(func() time.Time { return now })
	s.Enqueue("x", "x:1", "{}", 0)
	for i := 0; i < MaxAttempts; i++ {
		jobs, _ := s.Claim(1, 60)
		if len(jobs) != 1 {
			t.Fatalf("attempt %d: no job", i)
		}
		s.Retry(jobs[0], errors.New("nope"))
		now = now.Add(2 * time.Hour)
	}
	j, _ := s.JobByKey("x:1")
	if j.FailedAt == 0 {
		t.Fatalf("job not failed: %+v", j)
	}
}

func TestSessionsAndLogins(t *testing.T) {
	s := open(t)
	if _, err := s.Credentials(); !errors.Is(err, ErrNotFound) {
		t.Fatal("credentials before setup")
	}
	s.SetPassword("hash")
	c, _ := s.Credentials()
	if c.PasswordHash != "hash" || len(c.UserHandle) != 32 {
		t.Fatalf("creds = %+v", c)
	}
	s.CreateSession(&Session{TokenHash: "t", CSRF: "c", ExpiresAt: s.Unix() + 60})
	if se, err := s.Session("t"); err != nil || se.CSRF != "c" {
		t.Fatal(err)
	}
	s.DeleteSessions()
	if _, err := s.Session("t"); !errors.Is(err, ErrNotFound) {
		t.Fatal("session survived logout")
	}
	s.RecordLogin("1.2.3.4", false)
	s.RecordLogin("1.2.3.4", false)
	s.RecordLogin("5.6.7.8", true)
	if n, _ := s.FailedLogins("1.2.3.4", 0); n != 2 {
		t.Errorf("failed = %d", n)
	}
}
