package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// httpfs 必须排在 spatial/excel 之前：它们是按需安装 httpfs 的触发者，
// 顺序反了构建期照样失败。
func TestDownloadQueueOrdersDependenciesFirst(t *testing.T) {
	got := downloadQueue()
	if len(got) != len(duckdbExtensionDependencies)+len(duckdbExtensions) {
		t.Fatalf("queue has %d entries, want %d: %v",
			len(got), len(duckdbExtensionDependencies)+len(duckdbExtensions), got)
	}
	for i, dep := range duckdbExtensionDependencies {
		if got[i] != dep {
			t.Fatalf("queue[%d] = %q, want dependency %q (full queue: %v)", i, got[i], dep, got)
		}
	}
	for _, ext := range duckdbExtensions {
		if !contains(got, ext) {
			t.Fatalf("%q missing from queue: %v", ext, got)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// URL 必须是 .duckdb_extension.gz。少这个后缀就是线上踩过的坑：
// DuckDB 自己拼出来的是不带 .gz 的路径，官方仓库返回 404。
func TestExtensionArchiveURL(t *testing.T) {
	cases := []struct {
		name     string
		repo     string
		version  string
		platform string
		ext      string
		want     string
	}{
		{
			name:     "official repository",
			repo:     "https://extensions.duckdb.org",
			version:  "v1.5.2",
			platform: "linux_amd64",
			ext:      "httpfs",
			want:     "https://extensions.duckdb.org/v1.5.2/linux_amd64/httpfs.duckdb_extension.gz",
		},
		{
			name:     "trailing slash is normalised",
			repo:     "https://extensions.duckdb.org/",
			version:  "v1.5.2",
			platform: "linux_amd64",
			ext:      "spatial",
			want:     "https://extensions.duckdb.org/v1.5.2/linux_amd64/spatial.duckdb_extension.gz",
		},
		{
			name:     "surrounding whitespace is trimmed",
			repo:     "  https://extensions.duckdb.org  ",
			version:  "v1.5.2",
			platform: "linux_amd64",
			ext:      "excel",
			want:     "https://extensions.duckdb.org/v1.5.2/linux_amd64/excel.duckdb_extension.gz",
		},
		{
			name:     "internal mirror with a path prefix",
			repo:     "https://mirror.internal/duckdb",
			version:  "v1.5.2",
			platform: "linux_amd64",
			ext:      "httpfs",
			want:     "https://mirror.internal/duckdb/v1.5.2/linux_amd64/httpfs.duckdb_extension.gz",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extensionArchiveURL(tc.repo, tc.version, tc.platform, tc.ext)
			if got != tc.want {
				t.Fatalf("extensionArchiveURL(...) = %q, want %q", got, tc.want)
			}
			if !strings.HasSuffix(got, ".duckdb_extension.gz") {
				t.Fatalf("URL %q lost the .gz suffix", got)
			}
		})
	}
}

// 落盘位置必须和 DuckDB 自己 INSTALL 的布局一字不差，否则 LOAD 找不到。
// 基准来自生产报错：
//
//	Extension "/root/.duckdb/extensions/v1.5.2/linux_amd64/httpfs.duckdb_extension" not found
func TestExtensionTargetPathMatchesDuckDBLayout(t *testing.T) {
	got := extensionTargetPath("/root/.duckdb/extensions", "v1.5.2", "linux_amd64", "httpfs")
	want := filepath.Join("/root/.duckdb/extensions", "v1.5.2", "linux_amd64", "httpfs.duckdb_extension")
	if got != want {
		t.Fatalf("extensionTargetPath(...) = %q, want %q", got, want)
	}
	if strings.HasSuffix(got, ".gz") {
		t.Fatalf("target %q should be the decompressed file, not the archive", got)
	}
}
