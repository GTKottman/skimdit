// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import "context"

// StatKey is one aggregate counter: no visitor is ever identified.
type StatKey struct {
	Day      string
	Path     string
	Referrer string
	Kind     string
}

// AddStats adds counts in one transaction.
func (s *Store) AddStats(counts map[StatKey]int) error {
	if len(counts) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO stats_daily (day, path, referrer, kind, views) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(day, path, referrer, kind) DO UPDATE SET views = views + excluded.views`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for k, n := range counts {
		if _, err := stmt.Exec(k.Day, k.Path, k.Referrer, k.Kind, n); err != nil {
			stmt.Close()
			tx.Rollback()
			return err
		}
	}
	stmt.Close()
	return tx.Commit()
}

// Count is a label with a number.
type Count struct {
	Label string
	N     int
}

func (s *Store) counts(q string, args ...any) []Count {
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Count
	for rows.Next() {
		var c Count
		if rows.Scan(&c.Label, &c.N) == nil {
			out = append(out, c)
		}
	}
	return out
}

// DailyViews returns page views (or feed fetches) per day since a day.
func (s *Store) DailyViews(since, kind string) []Count {
	return s.counts(`SELECT day, SUM(views) FROM stats_daily WHERE day >= ? AND kind = ? GROUP BY day ORDER BY day`, since, kind)
}

// TopPaths returns the most viewed paths since a day.
func (s *Store) TopPaths(since, kind string, limit int) []Count {
	return s.counts(`SELECT path, SUM(views) FROM stats_daily WHERE day >= ? AND kind = ? GROUP BY path ORDER BY SUM(views) DESC, path LIMIT ?`, since, kind, limit)
}

// TopReferrers returns the domains that sent the most readers since a day.
func (s *Store) TopReferrers(since string, limit int) []Count {
	return s.counts(`SELECT referrer, SUM(views) FROM stats_daily WHERE day >= ? AND kind = 'page' AND referrer != '' GROUP BY referrer ORDER BY SUM(views) DESC, referrer LIMIT ?`, since, limit)
}
