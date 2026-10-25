// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

var svgElements = map[string]bool{
	"svg": true, "g": true, "path": true, "rect": true, "circle": true, "ellipse": true, "line": true,
	"polyline": true, "polygon": true, "text": true, "tspan": true, "textPath": true, "defs": true,
	"linearGradient": true, "radialGradient": true, "stop": true, "title": true, "desc": true,
	"clipPath": true, "mask": true, "use": true, "symbol": true, "pattern": true, "marker": true, "metadata": true,
	"filter": true, "feGaussianBlur": true, "feOffset": true, "feBlend": true, "feColorMatrix": true,
	"feMerge": true, "feMergeNode": true, "feFlood": true, "feComposite": true,
}

// CheckSVG accepts an SVG only when every element is on the allow-list,
// no attribute is an event handler, and every link points inside the file.
// Anything else is refused rather than silently rewritten.
func CheckSVG(data []byte) ([]byte, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	d.Entity = map[string]string{}
	seenRoot := false
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the SVG is not valid XML: %v", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			// No DOCTYPE at all: it can declare entities or default attributes (onload).
			_ = t
			return nil, errors.New("the SVG has a DOCTYPE or declaration, which is not allowed")
		case xml.ProcInst:
			if t.Target != "xml" {
				return nil, errors.New("the SVG has processing instructions, which are not allowed")
			}
		case xml.StartElement:
			if !seenRoot {
				if t.Name.Local != "svg" {
					return nil, errors.New("the file is not an SVG")
				}
				seenRoot = true
			}
			if !svgElements[t.Name.Local] {
				return nil, fmt.Errorf("the SVG contains <%s>, which is not allowed", t.Name.Local)
			}
			for _, a := range t.Attr {
				name := strings.ToLower(a.Name.Local)
				val := strings.ToLower(strings.TrimSpace(a.Value))
				if strings.HasPrefix(name, "on") {
					return nil, fmt.Errorf("the SVG has an event handler (%s)", a.Name.Local)
				}
				if name == "href" && !strings.HasPrefix(val, "#") {
					return nil, errors.New("the SVG links outside itself")
				}
				if strings.Contains(val, "javascript:") || strings.Contains(val, "data:text") ||
					(strings.Contains(val, "url(") && !strings.Contains(val, "url(#")) {
					return nil, errors.New("the SVG references something outside itself")
				}
			}
		case xml.CharData:
			if bytes.Contains(bytes.ToLower(t), []byte("@import")) {
				return nil, errors.New("the SVG imports styles")
			}
		}
	}
	if !seenRoot {
		return nil, errors.New("the file is not an SVG")
	}
	return data, nil
}
