// SPDX-License-Identifier: AGPL-3.0-or-later

// Package locale holds every UI string, so a site can translate or reword
// them in one file.
package locale

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

//go:embed en.toml
var english []byte

// Locale is a loaded set of strings.
type Locale struct {
	Months      []string          `toml:"months"`
	DateFormat  string            `toml:"date_format"`
	MonthFormat string            `toml:"month_format"`
	Strings     map[string]string `toml:"strings"`
}

// Load returns English overlaid with <siteRoot>/locales/<lang>.toml when present.
func Load(siteRoot, lang string) (*Locale, error) {
	var l Locale
	if _, err := toml.Decode(string(english), &l); err != nil {
		return nil, err
	}
	path := filepath.Join(siteRoot, "locales", lang+".toml")
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &l, nil
	}
	if err != nil {
		return nil, err
	}
	var over Locale
	md, err := toml.Decode(string(b), &over)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		return nil, fmt.Errorf("%s: unknown key %s", path, und[0])
	}
	if len(over.Months) == 12 {
		l.Months = over.Months
	} else if len(over.Months) != 0 {
		return nil, fmt.Errorf("%s: months must list 12 names", path)
	}
	if over.DateFormat != "" {
		l.DateFormat = over.DateFormat
	}
	if over.MonthFormat != "" {
		l.MonthFormat = over.MonthFormat
	}
	for k, v := range over.Strings {
		if _, ok := l.Strings[k]; !ok {
			return nil, fmt.Errorf("%s: unknown string %q", path, k)
		}
		l.Strings[k] = v
	}
	return &l, nil
}

// T returns a string with {name} placeholders filled from pairs: T("x", "n", 3).
func (l *Locale) T(key string, pairs ...any) string {
	s, ok := l.Strings[key]
	if !ok {
		return "[missing string: " + key + "]"
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		s = strings.ReplaceAll(s, "{"+fmt.Sprint(pairs[i])+"}", fmt.Sprint(pairs[i+1]))
	}
	return s
}

// Date formats a date with the locale's date format.
func (l *Locale) Date(t time.Time) string { return l.format(l.DateFormat, t) }

// Month formats a month heading such as "October 2026".
func (l *Locale) Month(t time.Time) string { return l.format(l.MonthFormat, t) }

// MonthName is the name of month m (1-12).
func (l *Locale) MonthName(m int) string {
	if m < 1 || m > 12 {
		return ""
	}
	return l.Months[m-1]
}

func (l *Locale) format(f string, t time.Time) string {
	r := strings.NewReplacer(
		"{month}", l.Months[t.Month()-1],
		"{monthNum}", fmt.Sprintf("%02d", int(t.Month())),
		"{dayPad}", fmt.Sprintf("%02d", t.Day()),
		"{day}", strconv.Itoa(t.Day()),
		"{year}", strconv.Itoa(t.Year()),
	)
	return r.Replace(f)
}
