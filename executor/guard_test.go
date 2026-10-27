package executor

import "testing"

func TestCheckGitArgs(t *testing.T) {
	aliases := map[string]string{"ci": "commit -v", "lg": "log --oneline", "sh": "!git push"}
	alias := func(n string) string { return aliases[n] }
	allowed := [][]string{
		{"status"}, {"pull", "--rebase", "--autostash"}, {"pull", "--ff-only"}, {"fetch", "--all"},
		{"checkout", "-B", "drupal-updater/temp-x"}, {"checkout", "--", "web/f"}, {"stash", "push", "-u", "-m", "x"},
		{"stash", "pop"}, {"branch", "-D", "drupal-updater/temp-x"}, {"remote", "add", "x", "y"},
		{"-C", "/tmp/x", "status"}, {"lg"}, {"rebase", "origin/main"}, {"reset", "--soft", "HEAD"}, {"config", "x", "y"},
	}
	refused := [][]string{
		{"commit", "-m", "x"}, {"push"}, {"merge", "x"}, {"cherry-pick", "abc"}, {"-C", "/tmp", "push"},
		{"-c", "user.name=x", "commit"}, {"-c", "alias.x=push", "x"}, {"ci"}, {"sh"}, {"pull"}, {"pull", "origin", "main"},
		{"pull", "--rebase=false"}, {"reset", "--hard"}, {"clean", "-fd"}, {"stash", "drop"}, {"stash", "clear"},
	}
	for _, a := range allowed {
		if err := checkGitArgs(a, alias); err != nil {
			t.Errorf("refused %v: %v", a, err)
		}
	}
	for _, a := range refused {
		if checkGitArgs(a, alias) == nil {
			t.Errorf("allowed %v", a)
		}
	}
}

func TestCheckDdevArgs(t *testing.T) {
	allowed := [][]string{
		{"start"}, {"stop"}, {"stop", "--unlist", "old-name"}, {"stop", "-a"}, {"snapshot", "--name", "pre-update"},
		{"snapshot", "restore", "--latest"}, {"composer", "update", "drupal/core-*", "-W"}, {"drush", "updb", "-y"},
		{"config", "--database=mysql:8.0"}, {"utility", "migrate-database", "mariadb:10.11"}, {"composer", "show", "--all"},
		{"import-db", "--file=db.sql.gz"}, {"exec", "ls"},
	}
	refused := [][]string{
		{"delete", "-Oy"}, {"clean"}, {"stop", "-R"}, {"stop", "--unlist", "x", "-aR"}, {"rm", "--remove-data"},
		{"start", "--reset-database"}, {"-j", "restart", "--reset-database"}, {"snapshot", "-C", "-y"},
		{"snapshot", "--name", "x", "-yC"}, {"snapshot", "--cleanup=true"},
	}
	for _, a := range allowed {
		if err := checkDdevArgs(a); err != nil {
			t.Errorf("refused %v: %v", a, err)
		}
	}
	for _, a := range refused {
		if checkDdevArgs(a) == nil {
			t.Errorf("allowed %v", a)
		}
	}
}
