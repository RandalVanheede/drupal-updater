package dashboard

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func site(name string) Hints { return Hints{Names: []Hint{{"Drupal site name", name}}} }

func TestResolve(t *testing.T) {
	projects := []Project{
		{ID: "1", Name: "Agora Group", Customer: "Agora Group"},
		{ID: "2", Name: "ADVN"},
		{ID: "3", Name: "ADVN Collectie"},
		{ID: "4", Name: "VAI"},
		{ID: "5", Name: "Febelfin Academy", Customer: "Febelfin Academy"},
		{ID: "6", Name: "Claeys & Engels Compensation", Customer: "Claeys & Engels"},
		{ID: "7", Name: "Dynamate"},
		{ID: "8", Name: "Acme"},
		{ID: "9", Name: "Hospital Physicists", Customer: "BHPA"},
	}
	generic := site("Dynamate") // the company's own site name on several unrelated projects
	locals := []Local{
		{Name: "agora", Hints: Hints{Names: []Hint{{"repository name", "agora-website"}}, Groups: []string{"agora"}}},
		{Name: "advn"},
		{Name: "collectie-vai"},
		{Name: "febelfin-academy"},
		{Name: "claeysengels-multisite", Hints: Hints{Groups: []string{"claeys-engels"}}},
		{Name: "busworld-a", Hints: generic},
		{Name: "busworld-b", Hints: generic},
		{Name: "busworld-c", Hints: generic},
		{Name: "acme-nl"},
		{Name: "acme-be"},
		{Name: "bhpa", Hints: Hints{Groups: []string{"corecrew"}}},
		{Name: "pinned"},
	}
	m := NewMatcher(locals, projects)
	got := m.Resolve(map[string]string{"pinned": "Hospital Physicists"})

	want := map[string]struct {
		id   string
		auto bool
	}{
		"agora":                  {"1", true},  // folder name inside the name, and the git group is the customer
		"advn":                   {"2", false}, // identical name
		"febelfin-academy":       {"5", false},
		"claeysengels-multisite": {"6", true},  // git group equals the customer, who has just this project
		"pinned":                 {"9", false}, // set in the configuration
	}
	for name, w := range want {
		g, ok := got[name]
		if !ok || g.Project.ID != w.id || g.Auto != w.auto || g.Reason == "" {
			t.Errorf("%s: got %+v (found %v), want project %s auto=%v", name, g, ok, w.id, w.auto)
		}
	}
	for _, name := range []string{"collectie-vai", "acme-nl", "acme-be", "busworld-a", "busworld-b", "busworld-c", "bhpa"} {
		if g, ok := got[name]; ok {
			t.Errorf("%s must not be guessed (ambiguous or generic), got %s because %s", name, g.Project.Name, g.Reason)
		}
	}
	// the unmatched ones still get sensible suggestions
	var names []string
	for _, p := range m.Suggest("collectie-vai", map[string]bool{"2": true}, 3) {
		names = append(names, p.Name)
	}
	if len(names) == 0 || names[0] != "VAI" { // the dashboard name inside the folder name: worth suggesting, not worth guessing
		t.Errorf("suggestions for collectie-vai: %v", names)
	}
	if s := m.Suggest("zzz-qqq", nil, 3); len(s) != 0 {
		t.Errorf("nothing similar must suggest nothing: %v", s)
	}
}

func TestMatchByIDAndTypos(t *testing.T) {
	ps := []Project{{ID: "abc", Name: "My Trucks"}, {ID: "x1", Name: "Twin"}, {ID: "x2", Name: "twin"}}
	m := NewMatcher([]Local{{Name: "my-truks"}, {Name: "twin"}}, ps)
	got := m.Resolve(nil)
	if _, ok := got["twin"]; ok {
		t.Error("two dashboard projects with the same name must not be guessed")
	}
	if _, ok := got["my-truks"]; ok {
		t.Error("a typo is only suggested, never matched on its own")
	}
	if s := m.Suggest("my-truks", nil, 3); len(s) == 0 || s[0].ID != "abc" {
		t.Errorf("typo suggestion: %v", s)
	}
	if g := NewMatcher([]Local{{Name: "t"}}, ps).Resolve(map[string]string{"t": "x2"})["t"]; g.Project.ID != "x2" {
		t.Errorf("override by id: %+v", g)
	}
}

func TestReadHints(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	write(".ddev/config.yaml", "name: agora-agora-website\ntype: drupal11\n")
	write(".git/config", "[core]\n\tbare = false\n[remote \"upstream\"]\n\turl = git@other:no/thanks.git\n[remote \"origin\"]\n\turl = git@gitlab.example.net:agora/sub/agora-website.git\n")
	write("config/sync/system.site.yml", "uuid: x\nname: 'Agora Group'\nmail: a@b.c\n")
	h := ReadHints(dir)
	got := map[string]string{}
	for _, n := range h.Names {
		got[n.Kind] = n.Value
	}
	if got["DDEV project"] != "agora-agora-website" || got["repository name"] != "agora-website" || got["Drupal site name"] != "Agora Group" {
		t.Errorf("names: %v", got)
	}
	if !reflect.DeepEqual(h.Groups, []string{"agora", "sub"}) {
		t.Errorf("groups: %v", h.Groups)
	}
	if h := ReadHints(t.TempDir()); len(h.Names) != 0 || len(h.Groups) != 0 {
		t.Errorf("an empty folder has no hints: %+v", h)
	}
}

func TestSplitRemote(t *testing.T) {
	for in, want := range map[string]string{
		"git@gitlab.com:corecrew/client-projects/drupal/8/bhpa.git": "bhpa|corecrew,client-projects,drupal,8",
		"https://gitlab.example.net/farm/barn.git":                  "barn|farm",
		"ssh://git@host.example:2222/team/app":                      "app|team",
		"":                                                          "|",
	} {
		repo, groups := splitRemote(in)
		if got := repo + "|" + strings.Join(groups, ","); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
