// SPDX-License-Identifier: AGPL-3.0-or-later

package server

func (s *Server) registerLaterJobs() {
	s.jobHandlers["ap_deliver"] = s.jobAPDeliver
	s.jobHandlers["ap_send"] = s.jobAPSend
	s.jobHandlers["mastodon"] = s.jobMastodon
	s.jobHandlers["bluesky"] = s.jobBluesky
}

func (s *Server) laterChannels() []string {
	var out []string
	if s.apOn() {
		out = append(out, "ap_deliver")
	}
	if s.cfg.Crosspost.Mastodon.Enabled {
		out = append(out, "mastodon")
	}
	if s.cfg.Crosspost.Bluesky.Enabled {
		out = append(out, "bluesky")
	}
	return out
}
