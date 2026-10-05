package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"

	"github.com/pandada8/pulumid/internal/store"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var consoleTemplate = template.Must(template.New("console").Funcs(template.FuncMap{"json": func(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }}).Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Pulumid</title><style>body{margin:0;background:#f4f6fa;color:#162538;font:15px system-ui}header{background:#15283e;color:white;padding:20px 30px}header a{color:white}main{max-width:1100px;padding:28px;margin:auto}nav{display:flex;gap:22px;margin:20px 0}a{color:#1765b3}article{background:white;border:1px solid #dde2e9;padding:24px;border-radius:9px;margin:16px 0}pre{white-space:pre-wrap;overflow-wrap:anywhere;font:13px monospace}input{padding:12px;width:70%}button{padding:12px;background:#1765b3;color:white;border:0;border-radius:4px}.status{color:#346845}table{width:100%;border-collapse:collapse}td,th{text-align:left;padding:10px;border-bottom:1px solid #ddd}.scroll{overflow-x:auto}</style></head><body><header><strong>Pulumid</strong> · self-hosted state console <a href="/stacks">Stacks</a></header><main>{{if .Login}}<article><h1>Sign in</h1><p>Enter a Pulumid API token. It is exchanged for an HttpOnly session.</p><form id="login"><input id="token" type="password" autocomplete="off" required aria-label="API token"><button>Sign in</button></form><p id="error" role="alert"></p></article><script>document.getElementById('login').onsubmit=async(e)=>{e.preventDefault();let r=await fetch('/console/api/session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token:document.getElementById('token').value})});document.getElementById('token').value='';if(r.ok){location.href='/stacks'}else{document.getElementById('error').textContent='Sign in failed'}};</script>{{else}}<nav><a href="{{.Base}}">Overview</a>{{if .Base}}<a href="{{.Base}}/resources">Resources</a><a href="{{.Base}}/history">History</a>{{end}}</nav><h1>{{.Title}}</h1>{{if .List}}<form action="/stacks" method="get"><label>Project <input name="project" value="{{.Project}}" placeholder="All projects"></label><button>Filter</button></form>{{end}}{{if .Stacks}}<article class="scroll"><table><thead><tr><th>Stack</th><th>Resources</th></tr></thead><tbody>{{range .Stacks}}<tr><td><a href="/{{.orgName}}/{{.projectName}}/{{.stackName}}">{{.orgName}} / {{.projectName}} / {{.stackName}}</a></td><td>{{.resourceCount}}</td></tr>{{end}}</tbody></table></article>{{else}}{{if .Summary}}<article><p class="status">Status: {{.Status}}</p><table><tr><th>Version</th><td>{{.Summary.version}}</td><th>Resources</th><td>{{.Summary.resourceCount}}</td></tr><tr><th>State generation</th><td>{{.Summary.headGeneration}}</td><th>Active update</th><td>{{.Summary.activeUpdate}}</td></tr></table></article>{{end}}{{if .Resources}}<article class="scroll"><h2>Resources</h2><table><tr><th>Resource</th><th>ID / Parent</th><th>Properties</th></tr>{{range .Resources}}<tr><td><strong>{{.type}}</strong><br>{{.urn}}</td><td>{{.id}}<br>{{.parent}}</td><td><details><summary>Inputs / Outputs</summary><pre>{{json .inputs}}</pre><pre>{{json .outputs}}</pre></details></td></tr>{{end}}</table></article>{{end}}{{if .History}}<article class="scroll"><h2>History</h2><table><tr><th>Version</th><th>Operation</th><th>Result</th><th>Message</th></tr>{{range .History}}<tr><td><a href="{{$.Base}}/updates/{{.version}}">{{.version}}</a></td><td>{{.kind}}</td><td>{{.result}}</td><td>{{.message}}</td></tr>{{end}}</table></article>{{end}}{{if .JSON}}<article><h2>Details</h2><pre>{{.JSON}}</pre></article>{{end}}{{end}}{{if .NextURL}}<p><a href="{{.NextURL}}">Next page →</a></p>{{end}}{{if .EventsURL}}<article><h2>Events</h2><p id="collection">Loading logs…</p><pre id="events"></pre></article><script>let next='',seen=new Map();async function poll(){try{let r=await fetch('{{.EventsURL}}'+(next?'?cursor='+encodeURIComponent(next):''));if(!r.ok)throw new Error('Log request failed');let x=await r.json();for(let e of x.data.events)seen.set(e.sequence,e);document.getElementById('events').textContent=Array.from(seen.values()).sort((a,b)=>a.sequence-b.sequence).map(e=>JSON.stringify(e)).join('\n');next=x.data.continuationToken;document.getElementById('collection').textContent=next?'Logs are still being collected':'Log collection finished';if(next)setTimeout(poll,2000)}catch(e){document.getElementById('collection').textContent='Network error; retrying';setTimeout(poll,2000)}}poll();</script>{{end}}{{end}}</main></body></html>`))

type page struct {
	Login                                bool
	Title, Base, Status, JSON, EventsURL string
	Stacks                               []map[string]any
	Summary                              map[string]any
	Resources                            []any
	History                              []map[string]any
	NextURL                              string
	Project                              string
	List                                 bool
}

func (a *API) web(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
	if r.URL.Path == "/login" && r.Method == "GET" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		consoleTemplate.Execute(w, page{Login: true})
		return
	}
	isAPI := strings.HasPrefix(r.URL.Path, "/console/api/")
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
	view := page{Title: "Stacks"}
	e = a.Store.Tx(r.Context(), func(tx *sql.Tx) error {
		mutate := r.Method != "GET"
		if mutate && !a.sameOrigin(r) {
			return store.Fail(403, "Same-origin request required")
		}
		actor, e := a.Store.Session(r.Context(), tx, cookie.Value, r.Header.Get("X-CSRF-Token"), mutate)
		if e != nil {
			return e
		}
		if r.URL.Path == "/console/api/session" && r.Method == "DELETE" {
			return a.Store.Logout(r.Context(), tx, cookie.Value)
		}
		if r.Method != "GET" {
			return store.Fail(404, "Not found")
		}
		p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if isAPI {
			p = p[2:]
			switch {
			case len(p) == 1 && p[0] == "stacks":
				items, e := a.Store.ListStacks(r.Context(), tx, actor, "", r.URL.Query().Get("project"), "", "")
				if e != nil {
					return e
				}
				result, nextCursor, e = a.Store.PageStacks(r.Context(), tx, actor, r.URL.Query().Get("project"), r.URL.Query().Get("cursor"), items)
				return e
			case len(p) >= 2 && p[0] == "stacks":
				if _, e = uuid.Parse(p[1]); e != nil {
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
					if e != nil {
						return e
					}
				}
				if len(p) == 4 && p[2] == "updates" {
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
			case len(p) >= 2 && p[0] == "updates":
				if _, e = uuid.Parse(p[1]); e != nil {
					return store.Fail(404, "Not found")
				}
				m, e := a.Store.WebUpdate(r.Context(), tx, actor, p[1])
				if e != nil {
					return e
				}
				result = m
				if len(p) == 3 && p[2] == "events" {
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
		}
		if r.URL.Path == "/" || r.URL.Path == "/stacks" {
			items, e := a.Store.ListStacks(r.Context(), tx, actor, "", r.URL.Query().Get("project"), "", "")
			if e != nil {
				return e
			}
			view.List = true
			view.Project = r.URL.Query().Get("project")
			view.Stacks, nextCursor, e = a.Store.PageStacks(r.Context(), tx, actor, view.Project, r.URL.Query().Get("cursor"), items)
			if nextCursor != "" {
				view.NextURL = "/stacks?cursor=" + url.QueryEscape(nextCursor) + "&project=" + url.QueryEscape(view.Project)
			}
			if len(items) == 0 {
				view.JSON = "No stacks yet."
			}
			return e
		}
		if len(p) < 3 {
			return store.Fail(404, "Not found")
		}
		st, e := a.Store.Stack(r.Context(), tx, p[0], p[1], p[2], false)
		if e != nil {
			return e
		}
		if e = a.Store.Permission(r.Context(), tx, actor, st.OrgID, false, false); e != nil {
			return e
		}
		m, e := a.Store.WebStackResolved(r.Context(), tx, actor, st)
		if e != nil {
			return e
		}
		view.Title = st.Org + " / " + st.Project + " / " + st.Name
		view.Base = "/" + st.Org + "/" + st.Project + "/" + st.Name
		view.Status = m["status"].(string)
		view.Summary = m
		state, _ := m["state"].(map[string]any)
		outputs := map[string]any{}
		if resources, ok := state["resources"].([]any); ok {
			for _, v := range resources {
				if r, ok := v.(map[string]any); ok && r["type"] == "pulumi:pulumi:Stack" {
					if o, ok := r["outputs"].(map[string]any); ok {
						outputs = o
					}
				}
			}
		}
		data := any(map[string]any{"outputs": outputs})
		if len(p) == 4 {
			switch p[3] {
			case "resources":
				resources, _ := state["resources"].([]any)
				if resources == nil {
					resources = []any{}
				}
				view.Resources, nextCursor, e = a.Store.PageItems(r.Context(), tx, actor, "web-resources", st.ID+":"+strconv.FormatInt(m["headGeneration"].(int64), 10), "", r.URL.Query().Get("cursor"), resources)
				if e != nil {
					return e
				}
				if nextCursor != "" {
					view.NextURL = view.Base + "/resources?cursor=" + url.QueryEscape(nextCursor)
				}
				data = nil
			case "history":
				if rows, ok := m["history"].([]any); ok {
					for _, v := range rows {
						if row, ok := v.(map[string]any); ok {
							view.History = append(view.History, row)
						}
					}
				}
				data = nil
			default:
				return store.Fail(404, "Not found")
			}
		}
		if len(p) == 5 {
			var id string
			switch p[3] {
			case "updates":
				v, e := strconv.Atoi(p[4])
				if e != nil {
					return store.Fail(400, "Invalid version")
				}
				id, e = a.Store.UpdateIDForVersion(r.Context(), tx, st.ID, v)
				if e != nil {
					return e
				}
			case "previews":
				id = p[4]
			default:
				return store.Fail(404, "Not found")
			}
			u, e := a.Store.Update(r.Context(), tx, st.ID, id)
			if e != nil {
				return e
			}
			view.Status = u.Status
			data, e = a.Store.WebUpdate(r.Context(), tx, actor, id)
			if e != nil {
				return e
			}
			if detail, ok := data.(map[string]any); ok {
				delete(detail, "stack")
				delete(detail, "state")
			}
			view.EventsURL = "/console/api/updates/" + id + "/events"
		}
		if len(p) > 5 {
			return store.Fail(404, "Not found")
		}
		var b []byte
		if data != nil {
			b, e = json.MarshalIndent(data, "", "  ")
		}
		view.JSON = string(b)
		return e
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
		envelope := map[string]any{"data": result}
		if nextCursor != "" {
			envelope["nextCursor"] = nextCursor
		}
		write(w, 200, envelope)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	consoleTemplate.Execute(w, view)
}
func (a *API) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, e := url.Parse(a.Store.Config.ConsoleURL)
	if e != nil {
		return false
	}
	return origin == u.Scheme+"://"+u.Host
}
