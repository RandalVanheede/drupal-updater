package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
)

// Project is the part of a dashboard project that Drupal Updater shows.
type Project struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Platform        string   `json:"platform"`
	CoreVersion     string   `json:"coreVersion"`
	PHP             string   `json:"php"`
	AccountManager  string   `json:"accountManager"`
	SLA             string   `json:"sla"`
	Customer        string   `json:"customer"`
	LastScan        string   `json:"lastScan"`
	SecurityUpdates []string `json:"securityUpdates"` // modules (and "drupal" for core) with a pending security release
	SecurityKnown   bool     `json:"securityKnown"`   // SecurityUpdates was looked up (it is empty both for "none" and "not asked")
	EOLRuntimes     []string `json:"eolRuntimes"`
	DashboardURL    string   `json:"dashboardUrl"`
	Stale           bool     `json:"stale"` // the site's connector has not reported for days: the module data may be old
}

type queryResult struct {
	Count         int       `json:"count"`
	ReturnedCount int       `json:"returnedCount"`
	NextOffset    int       `json:"nextOffset"`
	Projects      []Project `json:"projects"`
}

func (c *Client) query(ctx context.Context, args map[string]any) ([]Project, error) {
	var all []Project
	for offset := 0; ; {
		a := map[string]any{"limit": 200, "offset": offset}
		for k, v := range args {
			a[k] = v
		}
		text, err := c.CallTool(ctx, "queryProjects", a)
		if err != nil {
			return nil, err
		}
		var r queryResult
		if err := json.Unmarshal([]byte(text), &r); err != nil {
			return nil, fmt.Errorf("dashboard: unreadable project list: %w", err)
		}
		all = append(all, r.Projects...)
		if r.ReturnedCount == 0 || len(all) >= r.Count || r.NextOffset <= offset {
			return all, nil
		}
		offset = r.NextOffset
	}
}

// DrupalProjects lists the dashboard's Drupal projects. The dashboard only returns a project's pending security
// modules when asked for them (securityUpdates filter), so that is a second query merged in by id; projects whose
// connector has not synced for days are marked Stale (the dashboard's own definition: no report in the last 3 days).
func (c *Client) DrupalProjects(ctx context.Context) ([]Project, error) {
	projects, err := c.query(ctx, map[string]any{"platform": "drupal"})
	if err != nil {
		return nil, err
	}
	if withSecurity, err := c.query(ctx, map[string]any{"platform": "drupal", "securityUpdates": true}); err == nil {
		pending := map[string][]string{}
		for _, p := range withSecurity {
			pending[p.ID] = p.SecurityUpdates
		}
		for i := range projects {
			projects[i].SecurityUpdates = pending[projects[i].ID]
			projects[i].SecurityKnown = true
		}
	}
	if stale, err := c.query(ctx, map[string]any{"platform": "drupal", "syncState": "outdated"}); err == nil {
		old := map[string]bool{}
		for _, p := range stale {
			old[p.ID] = true
		}
		for i := range projects {
			projects[i].Stale = old[projects[i].ID]
		}
	}
	return projects, nil
}

// Ref is what to store in a project's config to point at p: its name, or its id when another project has the
// same name once normalised (the name alone would then be ambiguous).
func Ref(p Project, projects []Project) string {
	for _, o := range projects {
		if o.ID != p.ID && normalise(o.Name) == normalise(p.Name) {
			return p.ID
		}
	}
	return p.Name
}
