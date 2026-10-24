package search

import (
	"slices"
	"testing"
	"time"
)

func TestTerm(t *testing.T) {
	for in, want := range map[string]string{
		"Deploying":     "deploy",
		"deploys":       "deploy",
		"the":           "",
		"x":             "",
		"2026":          "2026",
		"۲۰۲۶":          "2026",
		"علي":           "علی",
		"می\u200cخواهم": "میخواهم",
		"كتاب":          "کتاب",
		"K8s":           "k8s",
		"Ｆｕｌｌ":          "full",
	} {
		if got := Term(in); got != want {
			t.Errorf("Term(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWords(t *testing.T) {
	text := "Ship it, then deploy-the release!"
	var got []string
	for _, w := range Words(text) {
		got = append(got, text[w.Start:w.End])
	}
	if want := []string{"Ship", "it", "then", "deploy", "the", "release"}; !slices.Equal(got, want) {
		t.Fatalf("Words = %q, want %q", got, want)
	}
	if terms := Terms("release the release, releasing"); !slices.Equal(terms, []string{"releas"}) {
		t.Fatalf("Terms = %q", terms)
	}
}

func TestParse(t *testing.T) {
	q, err := Parse(`from:@Alice in:~town-square "release notes" deploy -staging on:2026-03-02`)
	if err != nil {
		t.Fatal(err)
	}
	if q.From != "alice" || q.In != "town-square" {
		t.Fatalf("filters: %+v", q)
	}
	if !slices.Equal(q.Terms, []string{"releas", "note", "deploy"}) || len(q.Phrases) != 1 || !slices.Equal(q.Exclude, []string{"stage"}) {
		t.Fatalf("terms: %+v", q)
	}
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	if !q.After.Equal(day) || !q.Before.Equal(day.Add(24*time.Hour)) {
		t.Fatalf("dates: %v %v", q.After, q.Before)
	}
	for _, bad := range []string{"the", "", "from:bad!name", "before:yesterday", "after:2026-01-02 before:2026-01-01 x1", "in:#Not/Valid"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) passed", bad)
		}
	}
	if q, err := Parse("in:general"); err != nil || len(q.Terms) != 0 {
		t.Fatalf("a channel alone: %v %+v", err, q)
	}
}

func TestMatch(t *testing.T) {
	q, _ := Parse(`"release notes" -draft`)
	for text, want := range map[string]bool{
		"The release notes are out":        true,
		"notes about the release":          false,
		"Release notes (draft)":            false,
		"Releasing notes for the new site": true,
	} {
		if got := q.Match(text); got != want {
			t.Errorf("Match(%q) = %t, want %t", text, got, want)
		}
	}
	if m := Matches("Deploying now, deploy later", []string{"deploy"}); len(m) != 2 {
		t.Fatalf("Matches = %v", m)
	}
}
