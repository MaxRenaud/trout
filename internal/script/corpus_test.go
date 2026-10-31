package script

import (
	"cmp"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestCorpus loads every script in tmp/msl-corpus, if it's there (real
// scripts, downloaded, never committed: NNScript, Peace & Protection...),
// and reports what trout can't run, by how many files need it. It never
// fails: it's the list of what to build next.
//
//	go test ./internal/script -run TestCorpus -v
func TestCorpus(t *testing.T) {
	root := "../../tmp/msl-corpus"
	if _, err := os.Stat(root); err != nil {
		t.Skip("no tmp/msl-corpus")
	}
	byProblem := map[string]map[string]bool{}
	files, clean, fatal := 0, 0, 0
	lineNo := regexp.MustCompile(`^line \d+: `)
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		ext := strings.ToLower(filepath.Ext(path))
		if err != nil || d.IsDir() || ext != ".mrc" && ext != ".nns" && ext != ".ppa" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.HasPrefix(strings.TrimSpace(string(src)), "%") {
			return nil // a variables file (/load -rv), not a script
		}
		files++
		problems := New(Options{}).Load(filepath.Base(path), string(src))
		if slices.ContainsFunc(problems, func(p Problem) bool { return strings.Contains(p.Text, "not an alias, on or ctcp") }) {
			// An aliases file, which mIRC loads with /load -a.
			problems = New(Options{}).LoadAs(filepath.Base(path), string(src), "aliases")
		}
		switch {
		case len(problems) == 0:
			clean++
		case slices.ContainsFunc(problems, func(p Problem) bool { return p.Fatal }):
			fatal++
		}
		for _, p := range problems {
			text := lineNo.ReplaceAllString(p.String(), "")
			if p.Fatal {
				text += " [fatal]"
			}
			if byProblem[text] == nil {
				byProblem[text] = map[string]bool{}
			}
			byProblem[text][path] = true
		}
		return nil
	})
	t.Logf("%d script files: %d with no problems, %d with only some, %d not loaded (fatal)",
		files, clean, files-clean-fatal, fatal)
	texts := slices.SortedFunc(maps.Keys(byProblem), func(a, b string) int {
		return cmp.Or(len(byProblem[b])-len(byProblem[a]), strings.Compare(a, b))
	})
	for i, text := range texts {
		if i == 60 {
			t.Logf("... and %d more", len(texts)-i)
			break
		}
		t.Logf("%4d files  %s", len(byProblem[text]), text)
	}
}
