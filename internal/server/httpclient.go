// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// errPrivateAddress stops outgoing requests (webmention checks, ActivityPub
// fetches) from reaching the server's own network.
var errPrivateAddress = errors.New("refusing to connect to a private or local address")

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() ||
		// carrier-grade NAT and other non-public ranges
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64) ||
		(ip.To4() != nil && ip.To4()[0] == 0))
}

// safeHTTPClient refuses private addresses at connect time (after DNS, so
// rebinding tricks do not help), and limits time and redirects.
func safeHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 8 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || !publicIP(ip) {
				return errPrivateAddress
			}
			return nil
		},
	}
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
		Proxy:                 nil,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return errors.New("redirect to a non-HTTP URL")
			}
			return nil
		},
	}
}

// readLimited reads at most max bytes of a response body.
func readLimited(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("response is larger than %d bytes", max)
	}
	return b, nil
}

// fetch GETs a URL with the server's client and a Skimdit user agent.
func (s *Server) fetch(ctx context.Context, url, accept string, max int64) ([]byte, http.Header, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, "", err
	}
	req.Header.Set("User-Agent", s.userAgent())
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, resp.Header, resp.Request.URL.String(), &statusError{url: url, code: resp.StatusCode}
	}
	b, err := readLimited(resp.Body, max)
	return b, resp.Header, resp.Request.URL.String(), err
}

func (s *Server) userAgent() string {
	return "Skimdit/" + versionString() + " (+" + s.cfg.AbsURL(s.cfg.Site.BasePath) + ")"
}
