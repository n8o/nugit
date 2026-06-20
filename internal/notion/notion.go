// Package notion projects nugit's git-authoritative knowledge corpus (ADRs/specs/
// lessons) OUTBOUND into a Notion database (ADR-0011 / SPEC-002): git is the source
// of truth, Notion a derived, read-mostly view. Idempotent upsert keyed by a stable
// nugit_git_id property — query the database, then CREATE (POST /v1/pages with a
// markdown body) or UPDATE (PATCH /v1/pages/:id/markdown with replace_content, a
// full overwrite). Request shapes verified against the live Notion Markdown API
// (launched 2026-02; Notion-Version 2026-03-11). The HTTP publish needs a token;
// the body-builders + doc collection are pure and testable without one.
package notion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

const (
	apiBase       = "https://api.notion.com/v1"
	notionVersion = "2026-03-11"
	// GitIDProp is the database property that keys each page to its git object, so
	// re-publishing updates in place instead of duplicating.
	GitIDProp = "nugit_git_id"
)

// Doc is one knowledge object to project: a stable git id, a title, a markdown body.
type Doc struct {
	GitID    string
	Title    string
	Markdown string
}

// CollectDocs loads the durable knowledge corpus (decisions/specs/lessons) as Docs,
// sorted by id for deterministic publishing.
func CollectDocs(repoDir string) ([]Doc, error) {
	objs, err := knowledge.Load(repoDir)
	if err != nil {
		return nil, err
	}
	var docs []Doc
	for i := range objs {
		o := objs[i]
		switch o.Type {
		case model.KindDecision, model.KindSpec, model.KindLesson:
			docs = append(docs, Doc{GitID: o.ID, Title: titleOf(o), Markdown: o.Body})
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].GitID < docs[j].GitID })
	return docs, nil
}

// --- request bodies (match the live Notion Markdown API) ---

// CreateBody is the POST /v1/pages body to create a doc as a page in databaseID.
func CreateBody(databaseID string, d Doc) map[string]any {
	return map[string]any{
		"parent": map[string]any{"database_id": databaseID},
		"properties": map[string]any{
			"title":   richText(d.Title),
			GitIDProp: richText(d.GitID),
		},
		"markdown": d.Markdown,
	}
}

// ReplaceBody is the PATCH /v1/pages/:id/markdown body to fully overwrite content.
func ReplaceBody(markdown string) map[string]any {
	return map[string]any{
		"type":            "replace_content",
		"replace_content": map[string]any{"new_str": markdown},
	}
}

// queryBody filters a database for the page whose nugit_git_id equals gitID.
func queryBody(gitID string) map[string]any {
	return map[string]any{
		"filter":    map[string]any{"property": GitIDProp, "rich_text": map[string]any{"equals": gitID}},
		"page_size": 1,
	}
}

func richText(s string) []map[string]any {
	return []map[string]any{{"type": "text", "text": map[string]any{"content": s}}}
}

// --- publisher (needs NOTION_TOKEN) ---

// Result reports what an upsert run did.
type Result struct {
	Created int
	Updated int
	Errors  []string
}

// Publish upserts every doc into databaseID: find-by-git-id, then update-in-place or
// create. Git always wins (replace_content full overwrite); a tool-side edit is
// reconciled away on the next publish (SPEC-002).
func Publish(token, databaseID string, docs []Doc) (Result, error) {
	c := &client{token: token, http: &http.Client{Timeout: 30 * time.Second}}
	var res Result
	for _, d := range docs {
		pageID, err := c.findPage(databaseID, d.GitID)
		if err != nil {
			res.Errors = append(res.Errors, d.GitID+": "+err.Error())
			continue
		}
		if pageID == "" {
			if err := c.do("POST", apiBase+"/pages", CreateBody(databaseID, d), nil); err != nil {
				res.Errors = append(res.Errors, d.GitID+" (create): "+err.Error())
				continue
			}
			res.Created++
		} else {
			if err := c.do("PATCH", apiBase+"/pages/"+pageID+"/markdown", ReplaceBody(d.Markdown), nil); err != nil {
				res.Errors = append(res.Errors, d.GitID+" (update): "+err.Error())
				continue
			}
			res.Updated++
		}
	}
	return res, nil
}

type client struct {
	token string
	http  *http.Client
}

func (c *client) findPage(databaseID, gitID string) (string, error) {
	var out struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := c.do("POST", apiBase+"/databases/"+databaseID+"/query", queryBody(gitID), &out); err != nil {
		return "", err
	}
	if len(out.Results) > 0 {
		return out.Results[0].ID, nil
	}
	return "", nil
}

func (c *client) do(method, url string, body any, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(method, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", notionVersion)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return fmt.Errorf("notion %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// titleOf is the object's first markdown heading (its title), else its id.
func titleOf(o model.KnowledgeObject) string {
	for _, line := range strings.Split(o.Body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			return strings.TrimSpace(strings.TrimLeft(t, "# "))
		}
	}
	return o.ID
}
