// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"strings"
	"testing"
)

func FuzzSanitizeSVG(f *testing.F) {
	f.Add([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`))
	f.Add([]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="x"/>`))
	f.Add([]byte(`<svg><script>x</script></svg>`))
	f.Fuzz(func(t *testing.T, b []byte) {
		out, err := sanitizeSVG(b)
		if err != nil {
			return
		}
		low := strings.ToLower(string(out))
		for _, bad := range []string{"<script", "javascript:", "<foreignobject", "<iframe", "<!entity"} {
			if strings.Contains(low, bad) {
				t.Fatalf("accepted SVG contains %q: %q", bad, out)
			}
		}
	})
}

func FuzzParseSignature(f *testing.F) {
	f.Add(`keyId="https://a/#k",algorithm="rsa-sha256",headers="(request-target) host date",signature="YWJj"`)
	f.Add(`keyId=",",signature=""`)
	f.Fuzz(func(t *testing.T, s string) { _, _ = parseSignature(s) })
}

func FuzzParseBounce(f *testing.F) {
	f.Add([]byte("Final-Recipient: rfc822; a@b.c\r\nStatus: 5.1.1\r\n"))
	f.Add([]byte("Feedback-Type: abuse\r\nOriginal-Rcpt-To: <x@y.z>\r\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if r := parseBounce(b); r != nil && !strings.Contains(r.Email, "@") {
			t.Fatalf("bounce without an address: %+v", r)
		}
	})
}
