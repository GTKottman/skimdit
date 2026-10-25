// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	cases := []struct {
		args     []string
		code     int
		inStdout string
		inStderr string
	}{
		{[]string{"version"}, 0, "skimdit ", ""},
		{[]string{"help"}, 0, "Usage:", ""},
		{nil, 2, "", "Usage:"},
		{[]string{"nope"}, 2, "", `unknown command "nope"`},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		code := run(c.args, &out, &errOut)
		if code != c.code {
			t.Errorf("run(%v) code = %d, want %d", c.args, code, c.code)
		}
		if !strings.Contains(out.String(), c.inStdout) {
			t.Errorf("run(%v) stdout = %q, want it to contain %q", c.args, out.String(), c.inStdout)
		}
		if !strings.Contains(errOut.String(), c.inStderr) {
			t.Errorf("run(%v) stderr = %q, want it to contain %q", c.args, errOut.String(), c.inStderr)
		}
	}
}
