package main

import (
	"strings"
	"testing"
)

func TestQuoteSQLLiteral(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no quote", "https://extensions.duckdb.org", "https://extensions.duckdb.org"},
		{"single quote", "https://host/o'brien", "https://host/o''brien"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteSQLLiteral(tc.in); got != tc.want {
				t.Fatalf("quoteSQLLiteral(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildInstallStatement(t *testing.T) {
	cases := []struct {
		name string
		ext  string
		repo string
		want string
	}{
		{
			name: "empty repo keeps upstream form",
			ext:  "spatial",
			repo: "",
			want: "INSTALL spatial;",
		},
		{
			name: "repo pins the statement",
			ext:  "spatial",
			repo: "https://extensions.duckdb.org",
			want: "INSTALL spatial FROM 'https://extensions.duckdb.org';",
		},
		{
			name: "repo cannot break out of the literal",
			ext:  "excel",
			repo: "https://host/x'y",
			want: "INSTALL excel FROM 'https://host/x''y';",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildInstallStatement(tc.ext, tc.repo); got != tc.want {
				t.Fatalf("buildInstallStatement(%q, %q) = %q, want %q", tc.ext, tc.repo, got, tc.want)
			}
		})
	}
}

func TestBuildRepositoryStatements(t *testing.T) {
	t.Run("empty repo yields nothing", func(t *testing.T) {
		for _, repo := range []string{"", "   "} {
			if got := buildRepositoryStatements(repo); got != nil {
				t.Fatalf("buildRepositoryStatements(%q) = %v, want nil", repo, got)
			}
		}
	})

	t.Run("both spellings are emitted", func(t *testing.T) {
		got := buildRepositoryStatements("https://extensions.duckdb.org")
		want := []string{
			"SET autoinstall_extension_repository = 'https://extensions.duckdb.org';",
			"SET custom_extension_repository = 'https://extensions.duckdb.org';",
		}
		if len(got) != len(want) {
			t.Fatalf("got %d statements, want %d: %v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("statement %d = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("repo is escaped", func(t *testing.T) {
		for _, stmt := range buildRepositoryStatements("https://host/o'brien") {
			if !strings.Contains(stmt, "o''brien") {
				t.Fatalf("statement %q does not escape the quote", stmt)
			}
		}
	})
}

// The on-demand repository setting is what the failure hinges on: without it,
// httpfs is fetched over the compile-time default of plain HTTP. Guard the two
// names against an accidental rename.
func TestRepositorySettingNames(t *testing.T) {
	got := buildRepositoryStatements("https://example.test")
	joined := strings.Join(got, " ")
	for _, name := range []string{"autoinstall_extension_repository", "custom_extension_repository"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("%s missing from generated statements: %v", name, got)
		}
	}
}
