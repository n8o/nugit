// Package review serves the local approval queue: a browser UI over the
// candidate lane (ADR-0016), backed by the same `ratify` code path the CLI
// uses, writing to the working tree and nothing else.
//
// Why a server and not a rendered report: approving is a loop — read the
// record, decide, see the next one — and a report cannot close it. The
// candidate lane proved that by rotting: 14 decisions sat proposed across two
// stores, the oldest 100 days, because deciding meant reading front-matter in
// an editor and then retyping an id into a command.
//
// It is deliberately NOT a service. It binds loopback, is started by hand, runs
// for as long as the review takes, holds no state of its own, and mutates only
// files already in the store. Nothing it does is part of the delta path
// (ADR-0006): the engine never calls it, and the repo renders identically
// whether it has ever run.
package review

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/ratify"
)

//go:embed ui.html
var ui embed.FS

// Item is one record awaiting a decision, as the UI receives it.
type Item struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	Scope      string `json:"scope"`
	Created    string `json:"created"`
	AgeDays    int    `json:"ageDays"`
	Path       string `json:"path"`
	Confidence string `json:"confidence"`
	Context    string `json:"context"`
	Decision   string `json:"decision"`
	Rejected   string `json:"rejected"`
	// Ratifies is the status accepting this record would grant it, so the UI
	// can say what the button does rather than implying every type becomes
	// `accepted` (a lesson becomes `active`).
	Ratifies string `json:"ratifies"`
}

// Queue is the payload behind GET /api/queue.
type Queue struct {
	Repo    string   `json:"repo"`
	Branch  string   `json:"branch"`
	Items   []Item   `json:"items"`
	Changed []string `json:"changed"` // store files this session has modified
	Error   string   `json:"error,omitempty"`
}

// Server is the loopback review server.
type Server struct {
	dir string
	// token guards every mutating route. Loopback already keeps other machines
	// out, but any local process can POST to a local port, and these routes
	// edit and delete files in the user's repo — so the URL carries a secret
	// minted per run and the handlers require it.
	token string
	// changed accumulates the store paths this run has written, so the UI can
	// show the review's footprint and the exact command to commit it. The
	// server is single-user and single-run; a map with no lock would still be
	// touched from several handler goroutines, so it is guarded by the mutex in
	// changedMu.
	changed   map[string]string // path -> what happened
	changedMu chan struct{}     // 1-buffered channel used as a mutex
}

// New builds a server rooted at a nugit repo.
func New(dir string) (*Server, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(abs, ".nugit")); err != nil {
		return nil, fmt.Errorf("no .nugit/ under %s — run `nugit init` first", abs)
	}
	tok, err := mintToken()
	if err != nil {
		return nil, err
	}
	s := &Server{
		dir:       abs,
		token:     tok,
		changed:   map[string]string{},
		changedMu: make(chan struct{}, 1),
	}
	s.changedMu <- struct{}{}
	return s, nil
}

func mintToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) lock()   { <-s.changedMu }
func (s *Server) unlock() { s.changedMu <- struct{}{} }

// Listen binds loopback on port (0 picks a free one) and returns the listener
// plus the URL to open. Loopback only, never a routable interface: this process
// can delete files in the repo.
func (s *Server) Listen(port int) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, "", err
	}
	return ln, fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), s.token), nil
}

// Handler is the route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.serveUI)
	mux.HandleFunc("/api/queue", s.handleQueue)
	mux.HandleFunc("/api/decide", s.handleDecide)
	return mux
}

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := ui.ReadFile("ui.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The page is served from a loopback port and loads nothing external; say
	// so, so a stray reference cannot quietly reach the network.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src data:")
	w.Write(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	if !s.authed(w, r) {
		return
	}
	q := Queue{Repo: filepath.Base(s.dir)}
	repo := gitutil.Repo{Dir: s.dir}
	q.Branch = repo.CurrentBranch()

	pending, err := ratify.Pending(s.dir)
	if err != nil {
		q.Error = err.Error()
		writeJSON(w, http.StatusOK, q)
		return
	}
	for _, o := range pending {
		q.Items = append(q.Items, itemOf(o))
	}
	sort.Slice(q.Items, func(i, j int) bool {
		if q.Items[i].AgeDays != q.Items[j].AgeDays {
			return q.Items[i].AgeDays > q.Items[j].AgeDays // oldest debt first
		}
		return q.Items[i].ID < q.Items[j].ID
	})
	s.lock()
	for p, what := range s.changed {
		q.Changed = append(q.Changed, what+" "+p)
	}
	s.unlock()
	sort.Strings(q.Changed)
	writeJSON(w, http.StatusOK, q)
}

type decideReq struct {
	ID     string `json:"id"`
	Action string `json:"action"` // accept | decline
}

type decideResp struct {
	OK      string `json:"ok,omitempty"`
	Error   string `json:"error,omitempty"`
	Changed string `json:"changed,omitempty"`
}

func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	if !s.authed(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, decideResp{Error: "POST only"})
		return
	}
	var req decideReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, decideResp{Error: "unreadable request"})
		return
	}
	switch req.Action {
	case "accept":
		res, err := ratify.Ratify(s.dir, req.ID)
		if err != nil {
			writeJSON(w, http.StatusOK, decideResp{Error: err.Error()})
			return
		}
		s.note(res.Path, "ratified")
		writeJSON(w, http.StatusOK, decideResp{
			OK:      fmt.Sprintf("%s is now %s", res.ID, res.To),
			Changed: res.Path,
		})
	case "decline":
		path, err := s.decline(req.ID)
		if err != nil {
			writeJSON(w, http.StatusOK, decideResp{Error: err.Error()})
			return
		}
		s.note(path, "removed")
		writeJSON(w, http.StatusOK, decideResp{
			OK:      fmt.Sprintf("%s withdrawn from the candidate lane", req.ID),
			Changed: path,
		})
	default:
		writeJSON(w, http.StatusBadRequest, decideResp{Error: "action must be accept or decline"})
	}
}

// decline removes a candidate from the store.
//
// There is deliberately no `rejected` status. The nearest existing one,
// `invalidated`, is for a record that WAS live and is now void: a declined
// candidate scoped to a component would then make `stale-knowledge` warn on
// every PR touching that component forever, which is how a check earns a mute.
// A candidate that is not adopted simply leaves the lane, and git keeps it —
// `git show` on the deleting commit is the archive, and a rejected direction
// worth keeping belongs in the `Rejected` section of whatever decision replaced
// it, where retrieval will actually serve it.
func (s *Server) decline(id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", errors.New("no id")
	}
	objs, err := knowledge.Load(s.dir)
	if err != nil {
		return "", err
	}
	var match *model.KnowledgeObject
	for i := range objs {
		if objs[i].ID == id {
			if match != nil {
				return "", fmt.Errorf("%s is carried by more than one file — resolve the collision first (nugit doctor)", id)
			}
			match = &objs[i]
		}
	}
	if match == nil {
		return "", fmt.Errorf("no object with id %s", id)
	}
	if match.Status != model.StatusProposed {
		return "", fmt.Errorf("%s is %s, not proposed — only a candidate can be withdrawn", id, match.Status)
	}
	// Resolve under the repo root and refuse anything that escapes it: the path
	// comes from a parsed store file, but this handler deletes, so it verifies
	// rather than trusts.
	full := filepath.Join(s.dir, filepath.FromSlash(match.Path))
	rel, err := filepath.Rel(s.dir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to touch %s", match.Path)
	}
	if err := os.Remove(full); err != nil {
		return "", err
	}
	return match.Path, nil
}

func (s *Server) note(path, what string) {
	s.lock()
	s.changed[path] = what
	s.unlock()
}

// authed enforces the per-run token on every route.
func (s *Server) authed(w http.ResponseWriter, r *http.Request) bool {
	t := r.URL.Query().Get("t")
	if t == "" {
		t = r.Header.Get("X-Nugit-Token")
	}
	if subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1 {
		return true
	}
	http.Error(w, "bad or missing token — open the URL nugit printed", http.StatusForbidden)
	return false
}

var sectionCache = map[string]*regexp.Regexp{}

// section pulls one `## Name` block out of a record body, flattened to a single
// paragraph and trimmed. The UI shows an excerpt to decide from; the file is
// the full text, and its path is on the card.
func section(body string, names ...string) string {
	for _, n := range names {
		re, ok := sectionCache[n]
		if !ok {
			// (?m) is load-bearing: without it `^` anchors to the start of the
			// whole body, not to a line, and every section comes back empty.
			re = regexp.MustCompile(`(?ism)^#{2,}[ \t]*` + regexp.QuoteMeta(n) + `\b[^\n]*\n(.*?)(?:\n#{2}[ \t]|\z)`)
			sectionCache[n] = re
		}
		m := re.FindStringSubmatch(body)
		if m == nil {
			continue
		}
		t := strings.Join(strings.Fields(m[1]), " ")
		if t != "" {
			return truncate(t, 900)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndex(s[:n], " ")
	if cut < n/2 {
		cut = n
	}
	return s[:cut] + "…"
}

var titleRE = regexp.MustCompile(`(?m)^#\s+(.+)$`)

func itemOf(o model.KnowledgeObject) Item {
	title := ""
	if m := titleRE.FindStringSubmatch(o.Body); m != nil {
		title = strings.TrimSpace(m[1])
		// Records title themselves "ADR-0041 — the decision"; the id is already
		// its own column, so drop the duplicated prefix.
		if i := strings.Index(title, "—"); i > 0 && strings.Contains(title[:i], o.ID) {
			title = strings.TrimSpace(title[i+len("—"):])
		}
	}
	if title == "" {
		title = o.ID
	}
	age, created := 0, ""
	if !o.Created.IsZero() {
		age = int(time.Since(o.Created).Hours() / 24)
		created = o.Created.Format("2006-01-02")
	}
	// What accepting GRANTS comes from ratify, never a copy of its table here:
	// a lesson becomes `active`, a decision `accepted`, and a button that says
	// the wrong one is a second implementation waiting to disagree.
	grants := ""
	if st, err := ratify.GrantedStatus(o.Type); err == nil {
		grants = string(st)
	}
	return Item{
		ID: o.ID, Type: string(o.Type), Title: title, Scope: o.Scope,
		Created: created, AgeDays: age, Path: o.Path,
		Confidence: string(o.Confidence),
		Context:    section(o.Body, "Context", "Trigger"),
		Decision:   section(o.Body, "Decision", "Insight"),
		Rejected:   section(o.Body, "Rejected"),
		Ratifies:   grants,
	}
}

// PendingCount is what the command prints before it opens a browser, so a run
// with an empty lane says so on stdout instead of only in the page.
func (s *Server) PendingCount() (int, error) {
	p, err := ratify.Pending(s.dir)
	return len(p), err
}
