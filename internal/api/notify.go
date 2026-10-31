// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/napfkuchen1/mtmon/internal/alert"
)

// notifyView is what the settings page gets: the channels without secrets, only a flag whether one is stored.
type notifyView struct {
	Webhook struct {
		URL       string      `json:"url"`
		SecretSet bool        `json:"secret_set"`
		Quiet     alert.Quiet `json:"quiet"`
	} `json:"webhook"`
	Ntfy struct {
		URL   string      `json:"url"`
		Quiet alert.Quiet `json:"quiet"`
	} `json:"ntfy"`
	SMTP struct {
		Host    string      `json:"host"`
		Port    int         `json:"port"`
		User    string      `json:"user"`
		PassSet bool        `json:"pass_set"`
		From    string      `json:"from"`
		To      string      `json:"to"`
		Quiet   alert.Quiet `json:"quiet"`
	} `json:"smtp"`
}

func (s *Server) notifyOut(w http.ResponseWriter) {
	c := s.Alerts.Channels()
	var v notifyView
	v.Webhook.URL, v.Webhook.SecretSet, v.Webhook.Quiet = c.Webhook.URL, c.Webhook.Secret != "", c.Webhook.Quiet
	v.Ntfy.URL, v.Ntfy.Quiet = c.Ntfy.URL, c.Ntfy.Quiet
	v.SMTP.Host, v.SMTP.Port, v.SMTP.User, v.SMTP.PassSet = c.SMTP.Host, c.SMTP.Port, c.SMTP.User, c.SMTP.Pass != ""
	v.SMTP.From, v.SMTP.To, v.SMTP.Quiet = c.SMTP.From, c.SMTP.To, c.SMTP.Quiet
	jsonOut(w, v)
}

// notifyGet: GET /api/notify – channel settings (secrets are never sent) with their quiet hours.
func (s *Server) notifyGet(w http.ResponseWriter, r *http.Request) {
	if s.Alerts == nil {
		jerr(w, http.StatusServiceUnavailable, "alerts not available")
		return
	}
	s.notifyOut(w)
}

// notifySave: PUT /api/notify – replaces the channel settings; an empty secret or password keeps the stored one.
func (s *Server) notifySave(w http.ResponseWriter, r *http.Request) {
	if s.Alerts == nil {
		jerr(w, http.StatusServiceUnavailable, "alerts not available")
		return
	}
	var in alert.Channels
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.Alerts.SaveChannels(in); err != nil {
		jerr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.notifyOut(w)
}

// notifyTest: POST /api/notify/test?channel=webhook|ntfy|smtp – sends a test message at once (quiet hours do not apply).
func (s *Server) notifyTest(w http.ResponseWriter, r *http.Request) {
	if s.Alerts == nil {
		jerr(w, http.StatusServiceUnavailable, "alerts not available")
		return
	}
	ch := r.URL.Query().Get("channel")
	ok := false
	for _, n := range alert.ChannelNames {
		ok = ok || n == ch
	}
	if !ok {
		jerr(w, http.StatusBadRequest, "unknown channel")
		return
	}
	if err := s.Alerts.TestChannel(ch); err != nil {
		jsonOut(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	jsonOut(w, map[string]any{"ok": true})
}
