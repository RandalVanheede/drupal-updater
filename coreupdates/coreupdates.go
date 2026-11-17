// Package coreupdates tells how an installed Drupal core compares to the published releases
// (updates.drupal.org release history).
package coreupdates

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const FeedURL = "https://updates.drupal.org/release-history/drupal/current"

// Status values returned by Check.
const (
	UpToDate = "current"  // newest release of its major
	Behind   = "behind"   // newer release exists, none of them a security update
	Security = "security" // installed release is flagged insecure, or a newer one is a security update
)

type Release struct {
	Version  string
	Security bool // a security update
	Insecure bool // Drupal flags this release as insecure (a security update superseded it)
}

type Result struct {
	Status string `json:"status"`
	Latest string `json:"latest"` // newest stable release of the same major
}

type feed struct {
	Releases []struct {
		Version string `xml:"version"`
		Status  string `xml:"status"`
		Terms   []struct {
			Name  string `xml:"name"`
			Value string `xml:"value"`
		} `xml:"terms>term"`
	} `xml:"releases>release"`
}

// Fetch downloads the release history and returns the published stable releases.
func Fetch(ctx context.Context) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FeedURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, _ []*http.Request) error {
		if r.URL.Scheme != "https" {
			return errors.New("refusing non-https redirect")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release history: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	return Parse(body)
}

// Parse reads the release history XML; unpublished and pre-releases (alpha, beta, rc, dev) are skipped.
func Parse(data []byte) ([]Release, error) {
	var f feed
	if err := xml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	var out []Release
	for _, r := range f.Releases {
		if r.Status != "published" || strings.Contains(r.Version, "-") {
			continue
		}
		rel := Release{Version: r.Version}
		for _, t := range r.Terms {
			if t.Name != "Release type" {
				continue
			}
			rel.Security = rel.Security || t.Value == "Security update"
			rel.Insecure = rel.Insecure || t.Value == "Insecure"
		}
		out = append(out, rel)
	}
	return out, nil
}

// parse turns "11.4.10" (or "11.5.0-beta1") into its numeric parts; ok is false for anything else.
func parse(v string) (p [3]int, ok bool) {
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return p, false
	}
	for i, s := range parts {
		n, err := strconv.Atoi(s)
		if err != nil {
			return p, false
		}
		p[i] = n
	}
	return p, true
}

func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// Check compares an installed version with the releases. ok is false when the version is not a plain
// x.y.z (for instance a dev branch) or its major has no releases.
func Check(installed string, releases []Release) (Result, bool) {
	cur, ok := parse(installed)
	if !ok {
		return Result{}, false
	}
	var latest [3]int
	var latestV string
	secNewer, insecure := false, false
	for _, r := range releases {
		v, ok := parse(r.Version)
		if !ok || v[0] != cur[0] {
			continue
		}
		if newer(v, latest) {
			latest, latestV = v, r.Version
		}
		if r.Version == strings.TrimPrefix(installed, "v") && r.Insecure {
			insecure = true
		}
		if newer(v, cur) && r.Security {
			secNewer = true
		}
	}
	if latestV == "" {
		return Result{}, false
	}
	switch {
	case insecure || secNewer:
		return Result{Security, latestV}, true
	case newer(latest, cur):
		return Result{Behind, latestV}, true
	}
	return Result{UpToDate, latestV}, true
}
