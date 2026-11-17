package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"temproom/internal/app"
)

type APIHandler struct {
	svc *app.Service
}

func NewAPIHandler(svc *app.Service) *APIHandler { return &APIHandler{svc: svc} }

func decode(r *http.Request, v any) error {
	err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(v)
	if err == io.EOF {
		return nil
	}
	return err
}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	path := strings.TrimPrefix(r.URL.Path, "/api")
	send := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	fail := func(err error, code int) { http.Error(w, err.Error(), code) }

	switch {
	case path == "/status" && r.Method == http.MethodGet:
		send(h.svc.GetSystemStatus())

	case path == "/session" && r.Method == http.MethodGet:
		send(map[string]any{"session": h.svc.GetSession(), "ended": h.svc.LastEnded()})

	case path == "/room/create" && r.Method == http.MethodPost:
		var req struct {
			Proxy string `json:"proxy"`
		}
		if err := decode(r, &req); err != nil {
			fail(err, http.StatusBadRequest)
			return
		}
		sess, err := h.svc.CreateRoom(req.Proxy)
		if err != nil {
			fail(err, http.StatusBadRequest)
			return
		}
		send(sess)

	case path == "/room/join" && r.Method == http.MethodPost:
		var req struct {
			Invite string `json:"invite"`
		}
		if err := decode(r, &req); err != nil {
			fail(err, http.StatusBadRequest)
			return
		}
		sess, err := h.svc.JoinRoom(req.Invite)
		if err != nil {
			fail(err, http.StatusBadRequest)
			return
		}
		send(sess)

	case path == "/room/approve" && r.Method == http.MethodPost:
		sess, err := h.svc.ApproveJoin()
		if err != nil {
			fail(err, http.StatusBadRequest)
			return
		}
		send(sess)

	case path == "/relay" && r.Method == http.MethodGet:
		send(h.svc.RelayConfig())

	case path == "/relay" && r.Method == http.MethodPost:
		var req struct {
			Mode string `json:"mode"`
			URL  string `json:"url"`
		}
		if err := decode(r, &req); err != nil {
			fail(err, http.StatusBadRequest)
			return
		}
		cfg, health, err := h.svc.SetRelay(req.Mode, req.URL)
		out := map[string]any{"config": cfg, "health": health}
		if err != nil {
			out["error"] = err.Error()
		}
		send(out)

	case path == "/relay/check" && r.Method == http.MethodPost:
		var req struct {
			URL string `json:"url"`
		}
		if err := decode(r, &req); err != nil {
			fail(err, http.StatusBadRequest)
			return
		}
		send(h.svc.CheckRelay(req.URL))

	case path == "/proxy/probe" && r.Method == http.MethodGet:
		send(h.svc.ProbeLocalProxies())

	case path == "/proxy/check" && r.Method == http.MethodPost:
		var req struct {
			Address string `json:"address"`
		}
		if err := decode(r, &req); err != nil {
			fail(err, http.StatusBadRequest)
			return
		}
		send(h.svc.CheckProxyAddress(req.Address))

	case path == "/chrome/launch" && r.Method == http.MethodPost:
		var req struct {
			URL string `json:"url"`
		}
		if err := decode(r, &req); err != nil {
			fail(err, http.StatusBadRequest)
			return
		}
		if err := h.svc.LaunchChrome(req.URL); err != nil {
			fail(err, http.StatusConflict)
			return
		}
		send(map[string]any{"success": true})

	case path == "/chrome/close" && r.Method == http.MethodPost:
		if err := h.svc.CloseChrome(); err != nil {
			fail(err, http.StatusInternalServerError)
			return
		}
		send(map[string]any{"success": true})

	case path == "/leave" && r.Method == http.MethodPost:
		results, err := h.svc.LeaveRoom()
		if err != nil {
			fail(err, http.StatusInternalServerError)
			return
		}
		send(results)

	default:
		http.NotFound(w, r)
	}
}
