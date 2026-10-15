package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/pandada8/pulumi-api-backend/internal/store"
)

func (a *API) web(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if strings.HasPrefix(r.URL.Path, "/assets/") || (r.URL.Path == "/favicon.svg" || r.URL.Path == "/third-party-licenses.txt") {
		a.consoleAsset(w, r)
		return
	}
	isAPI := strings.HasPrefix(r.URL.Path, "/console/api/")
	if !isAPI && r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, store.Fail(405, "Method not allowed"))
		return
	}
	if r.URL.Path == "/login" {
		a.consoleShell(w, r)
		return
	}
	if r.URL.Path == "/console/api/session" && r.Method == "POST" {
		if !a.sameOrigin(r) {
			writeError(w, store.Fail(403, "Same-origin request required"))
			return
		}
		b, e := body(w, r)
		if e != nil {
			writeError(w, e)
			return
		}
		var req struct {
			Token string `json:"token"`
		}
		if e = json.Unmarshal(b, &req); e != nil {
			writeError(w, store.Fail(400, "Invalid login"))
			return
		}
		sid, csrf, e := a.Store.CreateSession(r.Context(), req.Token)
		if e != nil {
			writeError(w, e)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "pulumid_session", Value: sid, Path: "/", HttpOnly: true, Secure: !a.Store.Config.DevHTTP, SameSite: http.SameSiteLaxMode, MaxAge: 8 * 3600})
		write(w, 200, map[string]any{"data": map[string]any{"csrfToken": csrf}})
		return
	}
	cookie, e := r.Cookie("pulumid_session")
	if e != nil {
		if isAPI {
			writeError(w, store.Fail(401, "Sign in required"))
		} else {
			http.Redirect(w, r, "/login", 303)
		}
		return
	}
	var result any
	var nextCursor string
	e = a.Store.Tx(r.Context(), func(tx *sql.Tx) error {
		refresh := r.URL.Path == "/console/api/session/refresh" && r.Method == "POST"
		mutate := r.Method != "GET" && r.Method != "HEAD"
		if mutate && !a.sameOrigin(r) {
			return store.Fail(403, "Same-origin request required")
		}
		actor, e := a.Store.Session(r.Context(), tx, cookie.Value, r.Header.Get("X-CSRF-Token"), mutate && !refresh)
		if e != nil {
			return e
		}
		if refresh {
			csrf, e := a.Store.RefreshSessionCSRF(r.Context(), tx, cookie.Value)
			result = map[string]any{"csrfToken": csrf}
			return e
		}
		if r.URL.Path == "/console/api/session" {
			switch r.Method {
			case "GET":
				result = map[string]any{"login": actor.Login, "name": actor.Name}
				return nil
			case "DELETE":
				return a.Store.Logout(r.Context(), tx, cookie.Value)
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			return store.Fail(404, "Not found")
		}
		if !isAPI {
			return nil
		}
		p := strings.Split(strings.Trim(r.URL.EscapedPath(), "/"), "/")[2:]
		for i, v := range p {
			decoded, e := url.PathUnescape(v)
			if e != nil || strings.ContainsAny(decoded, "/\x00") || decoded == "." || decoded == ".." {
				return store.Fail(400, "Invalid path")
			}
			p[i] = decoded
		}
		switch {
		case len(p) == 1 && p[0] == "stacks":
			items, e := a.Store.ListStacks(r.Context(), tx, actor, "", r.URL.Query().Get("project"), "", "")
			if e != nil {
				return e
			}
			result, nextCursor, e = a.Store.PageStacks(r.Context(), tx, actor, r.URL.Query().Get("project"), r.URL.Query().Get("cursor"), items)
			return e
		case len(p) == 4 && p[0] == "resolve":
			st, e := a.Store.Stack(r.Context(), tx, p[1], p[2], p[3], false)
			if e != nil {
				return e
			}
			if e = a.Store.Permission(r.Context(), tx, actor, st.OrgID, false, false); e != nil {
				return e
			}
			result, e = a.Store.WebStackResolved(r.Context(), tx, actor, st)
			return e
		case len(p) >= 2 && p[0] == "stacks":
			if _, e = uuid.Parse(p[1]); e != nil {
				return store.Fail(404, "Not found")
			}
			if len(p) != 2 && !(len(p) == 3 && (p[2] == "resources" || p[2] == "history")) && !(len(p) == 4 && p[2] == "updates") {
				return store.Fail(404, "Not found")
			}
			m, e := a.Store.WebStack(r.Context(), tx, actor, p[1])
			if e != nil {
				return e
			}
			result = m
			if len(p) == 3 && p[2] == "resources" {
				resources, _ := m["state"].(map[string]any)["resources"].([]any)
				if resources == nil {
					resources = []any{}
				}
				result, nextCursor, e = a.Store.PageItems(r.Context(), tx, actor, "web-resources", p[1]+":"+strconv.FormatInt(m["headGeneration"].(int64), 10), "", r.URL.Query().Get("cursor"), resources)
				return e
			}
			if len(p) == 3 && p[2] == "history" {
				page := 1
				if value := r.URL.Query().Get("page"); value != "" {
					page, e = strconv.Atoi(value)
					if e != nil {
						return store.Fail(400, "Invalid page")
					}
				}
				result, e = a.Store.History(r.Context(), tx, store.Stack{ID: p[1]}, page, 100, false)
				// History includes config, so apply the same redaction as the stack response.
				if e == nil {
					result = redactConsole(result)
				}
				return e
			}
			if len(p) == 4 {
				version, e := strconv.Atoi(p[3])
				if e != nil {
					return store.Fail(400, "Invalid version")
				}
				id, e := a.Store.UpdateIDForVersion(r.Context(), tx, p[1], version)
				if e != nil {
					return e
				}
				result, e = a.Store.WebUpdate(r.Context(), tx, actor, id)
				return e
			}
			return nil
		case (len(p) == 2 || len(p) == 3 && p[2] == "events") && p[0] == "updates":
			if _, e = uuid.Parse(p[1]); e != nil {
				return store.Fail(404, "Not found")
			}
			m, e := a.Store.WebUpdate(r.Context(), tx, actor, p[1])
			if e != nil {
				return e
			}
			result = m
			if len(p) == 3 {
				u, e := a.Store.Update(r.Context(), tx, m["stack"].(map[string]any)["id"].(string), p[1])
				if e != nil {
					return e
				}
				result, e = a.Store.EventsRead(r.Context(), tx, actor, u, r.URL.Query().Get("cursor"), nil, "", false)
				return e
			}
			return nil
		}
		return store.Fail(404, "Not found")
	})
	if e != nil {
		var api *store.Error
		if !isAPI && errors.As(e, &api) && api.Code == 401 {
			http.Redirect(w, r, "/login", 303)
			return
		}
		writeError(w, e)
		return
	}
	if isAPI {
		if r.URL.Path == "/console/api/session" && r.Method == "DELETE" {
			http.SetCookie(w, &http.Cookie{Name: "pulumid_session", Value: "", Path: "/", HttpOnly: true, Secure: !a.Store.Config.DevHTTP, SameSite: http.SameSiteLaxMode, MaxAge: -1})
		}
		envelope := map[string]any{"data": result}
		if nextCursor != "" {
			envelope["nextCursor"] = nextCursor
		}
		write(w, 200, envelope)
		return
	}
	a.consoleShell(w, r)
}

func (a *API) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, e := url.Parse(a.Store.Config.ConsoleURL)
	return e == nil && origin == u.Scheme+"://"+u.Host
}
