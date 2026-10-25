// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/GTKottman/skimdit/internal/site"
	"github.com/GTKottman/skimdit/internal/store"
	"github.com/GTKottman/skimdit/internal/version"
)

func versionString() string { return version.Version }

// jobHandler runs one job and returns a short result for the activity log.
type jobHandler func(ctx context.Context, j *store.Job) (string, error)

func (s *Server) registerJobs() {
	s.jobHandlers["publish"] = s.jobPublish
	s.jobHandlers["announce"] = s.jobAnnounce
	s.registerFeatureJobs()
}

// RunJobs runs every due job once (the worker loop does this every few seconds).
func (s *Server) RunJobs(ctx context.Context) int {
	jobs, err := s.store.Claim(10, 300)
	if err != nil {
		s.log.Printf("jobs: %v", err)
		return 0
	}
	for _, j := range jobs {
		h := s.jobHandlers[j.Kind]
		if h == nil {
			_ = s.store.Retry(j, fmt.Errorf("no handler for job kind %q", j.Kind))
			continue
		}
		jctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
		res, err := h(jctx, j)
		cancel()
		if err != nil {
			s.log.Printf("job %s (%s) attempt %d failed: %v", j.Kind, j.Key, j.Attempts, err)
			_ = s.store.Retry(j, err)
			continue
		}
		_ = s.store.Finish(j.ID, res)
	}
	return len(jobs)
}

func (s *Server) workerLoop(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	var lastScan, lastClean time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := s.now()
		if now.Sub(lastScan) >= 20*time.Second {
			lastScan = now
			s.enqueueDueDrafts()
		}
		if now.Sub(lastClean) >= time.Hour {
			lastClean = now
			_ = s.store.Cleanup()
			_ = s.store.PruneJobs(30 * 86400)
		}
		for s.RunJobs(ctx) > 0 {
		}
	}
}

// enqueueDueDrafts turns scheduled drafts whose time has come into publish jobs.
func (s *Server) enqueueDueDrafts() {
	due, err := s.store.DueDrafts()
	if err != nil {
		s.log.Printf("scheduled drafts: %v", err)
		return
	}
	for _, d := range due {
		payload, _ := json.Marshal(map[string]int64{"draft": d.ID})
		_, _ = s.store.Enqueue("publish", fmt.Sprintf("publish:%d:%d", d.ID, d.PublishAt), string(payload), 0)
	}
}

func (s *Server) jobPublish(ctx context.Context, j *store.Job) (string, error) {
	var p struct {
		Draft int64 `json:"draft"`
	}
	if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
		return "", err
	}
	d, err := s.store.Draft(p.Draft)
	if err != nil {
		return "draft already gone", nil
	}
	if d.PublishAt == 0 || d.PublishAt > s.store.Unix() {
		return "no longer scheduled", nil
	}
	res, err := s.publishDraft(d)
	if err != nil {
		return "", err
	}
	return res.URL, nil
}

// announceWindow: posts older than this when first seen are never announced
// (imports, back-dated posts), so subscribers do not get a flood.
const announceWindow = 7 * 24 * time.Hour

// afterBuild queues the one-time announcement of each newly public post.
func (s *Server) afterBuild(b *site.Build) {
	first, _ := s.store.Enqueue("baseline", "baseline", "{}", 0)
	if first {
		// The first build ever: everything already published counts as announced.
		if j, err := s.store.JobByKey("baseline"); err == nil {
			_ = s.store.Finish(j.ID, "baseline")
		}
		for _, e := range b.Posts {
			_, _ = s.store.Enqueue("announce", "announce:"+e.Slug, "{}", 0)
			if j, err := s.store.JobByKey("announce:" + e.Slug); err == nil {
				_ = s.store.Finish(j.ID, "already published before the server started")
			}
		}
		return
	}
	for _, e := range b.Posts {
		if s.now().Sub(e.Date) > announceWindow {
			continue
		}
		payload, _ := json.Marshal(map[string]string{"slug": e.Slug})
		_, _ = s.store.Enqueue("announce", "announce:"+e.Slug, string(payload), 0)
	}
}

// jobAnnounce fans a new post out to every channel that is turned on. Each
// channel is its own job with its own key, so a retry never sends twice.
func (s *Server) jobAnnounce(ctx context.Context, j *store.Job) (string, error) {
	var p struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal([]byte(j.Payload), &p); err != nil || p.Slug == "" {
		return "nothing to announce", nil
	}
	n := 0
	for _, ch := range s.announceChannels() {
		payload, _ := json.Marshal(map[string]string{"slug": p.Slug})
		if added, err := s.store.Enqueue(ch, ch+":"+p.Slug, string(payload), 0); err != nil {
			return "", err
		} else if added {
			n++
		}
	}
	return fmt.Sprintf("%d channel(s)", n), nil
}
