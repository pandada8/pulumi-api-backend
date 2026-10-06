package httpapi

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/pandada8/pulumi-api-backend/internal/core"
	"github.com/pandada8/pulumi-api-backend/internal/faults"
	"github.com/pandada8/pulumi-api-backend/internal/pulumicompat"
	"github.com/pandada8/pulumi-api-backend/internal/store"
	"github.com/pulumi/pulumi/pkg/v3/util/validation"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type API struct{ Store *store.Store }

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if faults.Handle(w, r) {
		return
	}
	start := time.Now()
	recorded := &recordedWriter{ResponseWriter: w}
	w = recorded
	defer func() { recordRoute(r.URL.Path, time.Since(start), recorded.status) }()
	w.Header().Set("X-Request-ID", uuid.NewString())
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("panic request_id=%s", w.Header().Get("X-Request-ID"))
			if recorded.status == 0 {
				writeError(w, store.Fail(500, "Internal server error"))
			}
		}
	}()
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if r.URL.Path == "/metrics" && r.Method == "GET" {
			a.serveMetrics(w, r)
			return
		}
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		if r.URL.Path == "/readyz" {
			if a.Store.Ready(ctx) != nil {
				writeError(w, store.Fail(503, "Not ready"))
				return
			}
			write(w, 200, map[string]any{"ready": true})
			return
		}
		a.web(w, r)
		return
	}
	defer func() {
		log.Printf("request_id=%s method=%s elapsed_ms=%d", w.Header().Get("X-Request-ID"), r.Method, time.Since(start).Milliseconds())
	}()
	if r.URL.Path == "/api/cli/version" && r.Method == "GET" {
		write(w, 200, map[string]any{"latestVersion": pulumicompat.Version, "oldestWithoutWarning": pulumicompat.Version, "latestDevVersion": pulumicompat.Version})
		return
	}
	raw, e := body(w, r)
	if e != nil {
		writeError(w, e)
		return
	}
	status := 200
	var response any
	// Expiry is committed separately so a rejected writer cannot roll its expiry back.
	if e = faults.Hit(ctx, "after_candidate_before_lock"); e != nil {
		writeError(w, e)
		return
	}
	if e = a.Store.ExpirePath(ctx, r.URL.Path); e != nil {
		writeError(w, e)
		return
	}
	e = a.Store.Tx(ctx, func(tx *sql.Tx) error {
		auth := r.Header.Get("Authorization")
		lease := strings.HasPrefix(auth, "update-token ")
		token := strings.TrimPrefix(auth, "token ")
		if lease {
			token = strings.TrimPrefix(auth, "update-token ")
		}
		var actor store.Actor
		if !lease {
			if !strings.HasPrefix(auth, "token ") {
				return store.Fail(401, "Invalid credentials")
			}
			var e error
			actor, e = a.Store.Actor(ctx, tx, core.Digest([]byte(token)))
			if e != nil {
				return e
			}
		}
		parts := strings.Split(strings.Trim(r.URL.EscapedPath(), "/"), "/")
		for i, p := range parts {
			v, e := url.PathUnescape(p)
			if e != nil || strings.ContainsAny(v, "/\x00") || v == "." || v == ".." {
				return store.Fail(400, "Invalid path")
			}
			parts[i] = v
		}
		decode := func(v any) error {
			if len(raw) == 0 {
				return store.Fail(400, "Missing JSON request")
			}
			if e := pulumicompat.Decode(raw, v); e != nil {
				return store.Fail(400, "Invalid JSON request")
			}
			return nil
		}
		if len(parts) >= 2 && parts[1] == "user" && !lease {
			switch r.URL.Path {
			case "/api/user":
				if r.Method != "GET" {
					break
				}
				orgs, e := a.Store.Organizations(ctx, tx, actor)
				response = map[string]any{"id": actor.PrincipalID, "githubLogin": actor.Login, "name": actor.Name, "organizations": orgs}
				return e
			case "/api/user/organizations/default":
				orgs, e := a.Store.Organizations(ctx, tx, actor)
				if e != nil {
					return e
				}
				name := ""
				if len(orgs) > 0 {
					name = orgs[0]["githubLogin"].(string)
				}
				response = map[string]any{"GitHubLogin": name, "Messages": []any{}}
				return nil
			case "/api/user/stacks":
				items, e := a.Store.ListStacks(ctx, tx, actor, r.URL.Query().Get("organization"), r.URL.Query().Get("project"), r.URL.Query().Get("tagName"), r.URL.Query().Get("tagValue"))
				if e != nil {
					return e
				}
				q := r.URL.Query()
				q.Del("continuationToken")
				c, e := a.Store.Cursor(ctx, tx, actor, "stacks", "", q.Encode(), r.URL.Query().Get("continuationToken"))
				if e != nil {
					return e
				}
				filtered := []map[string]any{}
				for _, i := range items {
					if i["id"].(string) > c.Position {
						filtered = append(filtered, i)
					}
				}
				result := map[string]any{"stacks": filtered}
				if len(filtered) > 100 {
					result["stacks"] = filtered[:100]
					c.Position = filtered[99]["id"].(string)
					result["continuationToken"] = core.EncodeCursor(core.Purpose(a.Store.Config.Master, "cursor"), c)
				}
				response = result
				return nil
			}
		}
		if r.URL.Path == "/api/capabilities" && r.Method == "GET" && !lease {
			response = map[string]any{"capabilities": []any{map[string]any{"capability": "batch-encrypt"}}}
			return nil
		}
		if len(parts) < 4 || parts[1] != "stacks" {
			return store.Fail(404, "Not found")
		}
		org, project := parts[2], parts[3]
		if len(parts) == 4 {
			if lease {
				return store.Fail(403, "Invalid update token scope")
			}
			if r.Method == "HEAD" {
				items, e := a.Store.ListStacks(ctx, tx, actor, org, project, "", "")
				if e != nil {
					return e
				}
				if len(items) == 0 {
					return store.Fail(404, "Not found")
				}
				return nil
			}
			if r.Method == "POST" {
				var req apitype.CreateStackRequest
				if e := decode(&req); e != nil {
					return e
				}
				var m map[string]json.RawMessage
				json.Unmarshal(raw, &m)
				if c, ok := m["config"]; ok && string(c) != "null" && string(c) != "{}" {
					return store.Fail(422, "cloud config is unsupported")
				}
				if e := validation.ValidateStackTags(req.Tags); e != nil {
					return store.Fail(400, e.Error())
				}
				if e := a.Store.CreateStack(ctx, tx, actor, org, project, req, m["state"]); e != nil {
					return e
				}
				response = map[string]any{"messages": []any{}}
				return nil
			}
			return store.Fail(404, "Not found")
		}
		st, e := a.Store.Stack(ctx, tx, org, project, parts[4], true)
		if e != nil {
			return e
		}
		mutating := r.Method != "GET" && r.Method != "HEAD"
		secretDecrypt := len(parts) >= 6 && (parts[5] == "decrypt" || parts[5] == "batch-decrypt")
		if !lease {
			if e = a.Store.Permission(ctx, tx, actor, st.OrgID, mutating && !secretDecrypt, secretDecrypt); e != nil {
				return e
			}
		}
		if !st.Current {
			if !secretDecrypt {
				return store.Fail(404, "Not found")
			}
		}
		if len(parts) == 5 {
			if lease {
				return store.Fail(403, "Invalid update token scope")
			}
			switch r.Method {
			case "GET":
				var tags any
				json.Unmarshal(st.Tags, &tags)
				response = map[string]any{"id": st.ID, "orgName": st.Org, "projectName": st.Project, "stackName": st.Name, "activeUpdate": st.Active, "version": st.Version, "tags": tags}
				return nil
			case "DELETE":
				if e = a.Store.DeleteStack(ctx, tx, st, r.URL.Query().Get("force") == "true"); e != nil {
					return e
				}
				status = 204
				return nil
			}
			return store.Fail(404, "Not found")
		}
		action := parts[5]
		if action == "update" && len(parts) >= 7 {
			uid := parts[6]
			if _, e = uuid.Parse(uid); e != nil {
				return store.Fail(404, "Not found")
			}
			u, e := a.Store.Update(ctx, tx, st.ID, uid)
			if e != nil {
				return e
			}
			if len(parts) == 7 {
				switch r.Method {
				case "POST":
					if lease {
						return store.Fail(403, "User token required")
					}
					var req apitype.StartUpdateRequest
					if e = decode(&req); e != nil {
						return e
					}
					if e = validation.ValidateStackTags(req.Tags); e != nil {
						return store.Fail(400, e.Error())
					}
					out, e := a.Store.Start(ctx, tx, actor, &st, &u, req)
					response = out
					return e
				case "GET":
					if lease {
						return store.Fail(403, "User token required")
					}
					response, e = a.Store.EventsRead(ctx, tx, actor, u, r.URL.Query().Get("continuationToken"), nil, "", true)
					return e
				}
				return store.Fail(404, "Not found")
			}
			sub := strings.Join(parts[7:], "/")
			if sub == "events" && r.Method == "GET" && !lease {
				response, e = a.Store.EventsRead(ctx, tx, actor, u, r.URL.Query().Get("continuationToken"), r.URL.Query()["type"], r.URL.Query().Get("urn"), false)
				return e
			}
			if sub == "cancel" && r.Method == "POST" && !lease {
				status = 204
				return a.Store.Cancel(ctx, tx, st, u)
			}
			if !lease {
				return store.Fail(403, "Update token required")
			}
			switch sub {
			case "renew_lease":
				if r.Method != "POST" {
					break
				}
				var req apitype.RenewUpdateLeaseRequest
				if e = decode(&req); e != nil {
					return e
				}
				response, e = a.Store.Renew(ctx, tx, st, u, token, req.Duration)
				return e
			case "checkpoint":
				if r.Method != "PATCH" {
					break
				}
				var req apitype.PatchUpdateCheckpointRequest
				if e = decode(&req); e != nil {
					return e
				}
				status = 204
				return a.Store.SaveFull(ctx, tx, st, u, token, req)
			case "journalentries":
				if r.Method != "PATCH" {
					break
				}
				var req struct {
					Entries []json.RawMessage `json:"entries"`
				}
				if e = decode(&req); e != nil {
					return e
				}
				status = 204
				return a.Store.AppendJournal(ctx, tx, st, u, token, req.Entries)
			case "events/batch":
				if r.Method != "POST" {
					break
				}
				var req struct {
					Events []json.RawMessage `json:"events"`
				}
				if e = decode(&req); e != nil {
					return e
				}
				status = 204
				return a.Store.EventsWrite(ctx, tx, st, u, token, req.Events)
			case "complete":
				if r.Method != "POST" {
					break
				}
				var req struct {
					Status string `json:"status"`
				}
				if e = decode(&req); e != nil {
					return e
				}
				status = 204
				return a.Store.Complete(ctx, tx, st, u, token, req.Status)
			}
			return store.Fail(404, "Not found")
		}
		if lease {
			return store.Fail(403, "Invalid update token scope")
		}
		switch action {
		case "update", "preview", "refresh", "destroy":
			if len(parts) != 6 || r.Method != "POST" {
				break
			}
			var req apitype.UpdateProgramRequest
			if e = decode(&req); e != nil {
				return e
			}
			id, e := a.Store.CreateUpdate(ctx, tx, actor, st, action, req)
			response = map[string]any{"updateID": id, "requiredPolicies": []any{}, "messages": []any{}, "aiSettings": map[string]any{"copilotIsEnabled": false}}
			return e
		case "export":
			if r.Method != "GET" {
				break
			}
			var b []byte
			if len(parts) == 7 {
				v, e := strconv.Atoi(parts[6])
				if e != nil {
					return store.Fail(400, "Invalid version")
				}
				b, e = a.Store.ExportVersion(ctx, tx, st, v)
				if e != nil {
					return e
				}
			} else if len(parts) == 6 {
				b, _, e = a.Store.Head(ctx, tx, st.ID)
				if e != nil {
					return e
				}
			} else {
				break
			}
			response = json.RawMessage(b)
			return nil
		case "import":
			if r.Method != "POST" || len(parts) != 6 {
				break
			}
			id, e := a.Store.Import(ctx, tx, actor, st, raw, "import")
			response = map[string]any{"updateId": id}
			return e
		case "tags":
			if r.Method != "PATCH" {
				break
			}
			var tags map[apitype.StackTagName]string
			if e = decode(&tags); e != nil {
				return e
			}
			if e = validation.ValidateStackTags(tags); e != nil {
				return store.Fail(400, e.Error())
			}
			status = 204
			return a.Store.Tags(ctx, tx, st, tags)
		case "rename":
			if r.Method != "POST" {
				break
			}
			var req struct {
				NewName    string `json:"newName"`
				NewProject string `json:"newProject"`
			}
			if e = decode(&req); e != nil {
				return e
			}
			status = 204
			return a.Store.Rename(ctx, tx, actor, st, req.NewProject, req.NewName)
		case "updates":
			if r.Method != "GET" {
				break
			}
			latest := len(parts) == 7 && parts[6] == "latest"
			page, size := 1, 100
			if q := r.URL.Query().Get("page"); q != "" {
				page, _ = strconv.Atoi(q)
			}
			if q := r.URL.Query().Get("pageSize"); q != "" {
				size, _ = strconv.Atoi(q)
			}
			if latest {
				page, size = 1, 1
			}
			items, e := a.Store.History(ctx, tx, st, page, size, latest)
			if e != nil {
				return e
			}
			if latest {
				if len(items) == 0 {
					return store.Fail(404, "Not found")
				}
				response = map[string]any{"info": items[0]}
			} else {
				response = map[string]any{"updates": items}
			}
			return nil
		case "encrypt", "decrypt", "batch-encrypt", "batch-decrypt":
			if r.Method != "POST" {
				break
			}
			if len(parts) == 7 && action == "decrypt" && (parts[6] == "log-decryption" || parts[6] == "log-batch-decryption") {
				status = 204
				return a.Store.Audit(ctx, tx, actor, st, "decrypt")
			}
			var req struct {
				Plaintext   []byte   `json:"plaintext"`
				Ciphertext  []byte   `json:"ciphertext"`
				Plaintexts  [][]byte `json:"plaintexts"`
				Ciphertexts [][]byte `json:"ciphertexts"`
			}
			if e = decode(&req); e != nil {
				return e
			}
			decrypt := strings.Contains(action, "decrypt")
			if strings.HasPrefix(action, "batch-") {
				in := req.Plaintexts
				if decrypt {
					in = req.Ciphertexts
				}
				out := [][]byte{}
				m := map[string][]byte{}
				for _, b := range in {
					o, e := a.Store.Crypt(ctx, tx, st, b, decrypt)
					if e != nil {
						return e
					}
					out = append(out, o)
					m[base64.StdEncoding.EncodeToString(b)] = o
				}
				if decrypt {
					response = map[string]any{"plaintexts": m}
				} else {
					response = map[string]any{"ciphertexts": out}
				}
			} else {
				in := req.Plaintext
				if decrypt {
					in = req.Ciphertext
				}
				out, e := a.Store.Crypt(ctx, tx, st, in, decrypt)
				if e != nil {
					return e
				}
				key := "ciphertext"
				if decrypt {
					key = "plaintext"
				}
				response = map[string]any{key: out}
			}
			return a.Store.Audit(ctx, tx, actor, st, action)
		}
		return store.Fail(404, "Not found")
	})
	if e != nil {
		writeError(w, e)
		return
	}
	if e = faults.Hit(ctx, "after_db_commit_before_response"); e != nil {
		if h, ok := w.(http.Hijacker); ok {
			conn, _, err := h.Hijack()
			if err == nil {
				conn.Close()
				return
			}
		}
		writeError(w, e)
		return
	}
	write(w, status, response)
}
func write(w http.ResponseWriter, status int, v any) {
	if status == 204 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		if b, ok := v.(json.RawMessage); ok {
			w.Write(b)
		} else {
			json.NewEncoder(w).Encode(v)
		}
	}
}
func writeError(w http.ResponseWriter, e error) {
	e = store.Translate(e)
	var api *store.Error
	if !errors.As(e, &api) {
		log.Printf("internal error: %T", e)
		api = &store.Error{Code: 500, Message: "Internal server error"}
	}
	write(w, api.Code, map[string]any{"code": api.Code, "message": api.Message})
}
func body(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	compressed, expanded := int64(1<<20), int64(4<<20)
	if strings.Contains(r.URL.Path, "checkpoint") || strings.HasSuffix(r.URL.Path, "/import") {
		compressed, expanded = 32<<20, 128<<20
	}
	if strings.HasSuffix(r.URL.Path, "/journalentries") {
		compressed, expanded = 16<<20, 32<<20
	}
	if strings.HasSuffix(r.URL.Path, "/events/batch") {
		compressed, expanded = 4<<20, 16<<20
	}
	var reader io.Reader = http.MaxBytesReader(w, r.Body, compressed)
	if ce := r.Header.Get("Content-Encoding"); ce != "" {
		if ce != "gzip" {
			return nil, store.Fail(415, "Unsupported Content-Encoding")
		}
		g, e := gzip.NewReader(reader)
		if e != nil {
			return nil, store.Fail(400, "Invalid gzip")
		}
		defer g.Close()
		reader = g
	}
	b, e := io.ReadAll(io.LimitReader(reader, expanded+1))
	if e != nil {
		var max *http.MaxBytesError
		if errors.As(e, &max) {
			return nil, store.Fail(413, "Request too large")
		}
		return nil, store.Fail(400, "Invalid request body")
	}
	if int64(len(b)) > expanded {
		return nil, store.Fail(413, "Request too large")
	}
	if len(b) > 0 {
		var generic any
		if e = pulumicompat.Decode(b, &generic); e != nil {
			return nil, store.Fail(400, "Invalid JSON request")
		}
	}
	return b, nil
}
