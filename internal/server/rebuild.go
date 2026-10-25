// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/GTKottman/skimdit/internal/config"
	"github.com/GTKottman/skimdit/internal/site"
)

// requestRebuild asks the rebuild loop for a fresh build (coalesced).
func (s *Server) requestRebuild() {
	select {
	case s.rebuildCh <- struct{}{}:
	default:
	}
}

// rebuild builds the site now. Only one build runs at a time; the output
// is swapped in atomically, so readers never see a half-built site.
func (s *Server) rebuild() error {
	s.buildMu.Lock()
	defer s.buildMu.Unlock()
	start := time.Now()
	cfg, err := config.Load(s.root)
	if err != nil {
		s.setBuildError(err.Error())
		return err
	}
	// The server keeps its own config in memory; content settings come fresh.
	opts := site.Options{
		Root:       s.root,
		Config:     cfg,
		Out:        s.out,
		Now:        s.now(),
		RecordURLs: true,
		Hooks:      s.buildHooks(),
	}
	res, err := site.Run(opts)
	if err != nil {
		msg := err.Error()
		if res != nil && len(res.Diags) > 0 {
			msg = res.Diags.Errors().Error()
		}
		s.setBuildError(msg)
		return fmt.Errorf("build: %s", msg)
	}
	redirects := loadRedirectsFile(filepath.Join(s.out, "_redirects"))
	s.bmu.Lock()
	s.build = res.Build
	s.buildErr = ""
	s.buildAt = s.now()
	s.redirects = redirects
	s.nextChange = res.Build.NextChange
	s.bmu.Unlock()
	s.log.Printf("built %d entries in %s", res.Entries, time.Since(start).Round(time.Millisecond))
	s.afterBuild(res.Build)
	return nil
}

func (s *Server) setBuildError(msg string) {
	s.bmu.Lock()
	s.buildErr = msg
	s.bmu.Unlock()
	s.log.Printf("build failed: %s", msg)
}

// Build returns the last successful build.
func (s *Server) lastBuild() *site.Build {
	s.bmu.RLock()
	defer s.bmu.RUnlock()
	return s.build
}

// rebuildLoop runs requested builds and wakes up when a scheduled post's
// time comes.
func (s *Server) rebuildLoop(ctx context.Context) {
	for {
		s.bmu.RLock()
		next := s.nextChange
		s.bmu.RUnlock()
		var wake <-chan time.Time
		if !next.IsZero() {
			d := next.Sub(s.now()) + time.Second
			if d < time.Second {
				d = time.Second
			}
			if d > time.Hour {
				d = time.Hour
			}
			wake = time.After(d)
		} else {
			wake = time.After(time.Hour)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.rebuildCh:
			// Let a burst of changes settle.
			time.Sleep(150 * time.Millisecond)
			_ = s.rebuild()
		case <-wake:
			if !next.IsZero() && !s.now().Before(next) {
				_ = s.rebuild()
			}
		}
		select {
		case s.buildDoneCh <- struct{}{}:
		default:
		}
	}
}
