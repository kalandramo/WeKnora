package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/duckdb/duckdb-go/v2"
)

// EnvDuckDBExtensionRepository 覆盖 DuckDB 扩展仓库地址（可选）。
//
// DuckDB 默认从 http://extensions.duckdb.org 拉取扩展。在只允许 HTTPS 出网
// 的内网环境中，明文 HTTP 请求会被准入网关拦截并返回 HTML 认证页，于是
// INSTALL 拿到的不是 gzip 流，报错：
//
//	IO Error: Input is not a GZIP stream
//
// 将本变量设为 https://extensions.duckdb.org（或内网镜像）即可绕开。
// 留空则保持上游默认行为。
const EnvDuckDBExtensionRepository = "DUCKDB_EXTENSION_REPOSITORY"

// duckdbExtensions is the list of DuckDB extensions required by WeKnora's
// data analysis tool. `spatial` is used for layer metadata (st_read_meta)
// so we can enumerate sheet names from Excel files, while `excel` provides
// the dedicated read_xlsx reader with proper type inference.
var duckdbExtensions = []string{"spatial", "excel"}

// duckdbExtensionDependencies lists extensions that DuckDB installs on demand
// while installing the ones above. Installing `spatial` pulls in `httpfs`,
// and that on-demand install does not honour the repository given to
// INSTALL ... FROM: it reads a repository setting instead and falls back to
// the compile-time default of http://extensions.duckdb.org. Pre-installing
// them here keeps the on-demand path from reaching the network at all.
//
// Only used when EnvDuckDBExtensionRepository is set, so upstream behaviour
// (one INSTALL per extension, nothing else) is preserved otherwise.
var duckdbExtensionDependencies = []string{"httpfs"}

// quoteSQLLiteral escapes a value for a single-quoted SQL literal, so a
// repository URL containing an apostrophe cannot break the statement.
func quoteSQLLiteral(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// buildInstallStatement renders the INSTALL statement for ext. With an empty
// repo it keeps the upstream form, which uses DuckDB's built-in default.
func buildInstallStatement(ext, repo string) string {
	if repo == "" {
		return fmt.Sprintf("INSTALL %s;", ext)
	}
	return fmt.Sprintf("INSTALL %s FROM '%s';", ext, quoteSQLLiteral(repo))
}

// buildRepositoryStatements renders the SET statements that redirect DuckDB's
// on-demand installer — the path INSTALL ... FROM does not cover.
//
// Two spellings exist for the setting: autoinstall_extension_repository is the
// current name, custom_extension_repository the older one still accepted by
// 1.5.x. Which one a given build understands is not worth guessing, so both
// are returned and each failure is ignored: SET on an unknown parameter
// raises, and a missing legacy name must not abort the build.
func buildRepositoryStatements(repo string) []string {
	if strings.TrimSpace(repo) == "" {
		return nil
	}
	lit := quoteSQLLiteral(repo)
	return []string{
		fmt.Sprintf("SET autoinstall_extension_repository = '%s';", lit),
		fmt.Sprintf("SET custom_extension_repository = '%s';", lit),
	}
}

// installExtension installs and loads one extension, preferring repo over the
// built-in default. fatal controls whether a failure aborts the build: true
// for the extensions WeKnora actually calls, false for dependencies that may
// already be compiled in.
//
// When repo is set, a plain INSTALL is tried as a second attempt: it covers
// httpfs being unable to fetch itself, which would make the repository URL
// unusable for that one extension.
func installExtension(ctx context.Context, sqlDB *sql.DB, ext, repo string, fatal bool) bool {
	attempts := []string{buildInstallStatement(ext, repo)}
	if repo != "" {
		attempts = append(attempts, buildInstallStatement(ext, ""))
	}

	var lastErr error
	for _, stmt := range attempts {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			lastErr = err
			continue
		}
		if _, err := sqlDB.ExecContext(ctx, fmt.Sprintf("LOAD %s;", ext)); err != nil {
			lastErr = err
			continue
		}
		fmt.Printf("[duckdb] %s installed and loaded (%s)\n", ext, stmt)
		return true
	}

	if fatal {
		panic(fmt.Errorf("failed to install %s extension: %w", ext, lastErr))
	}
	fmt.Printf("[duckdb] WARNING: could not install %s (%v); continuing\n", ext, lastErr)
	return false
}

// setOnDemandRepository points DuckDB's on-demand installer at repo. Failures
// are reported but never fatal: an older build may know neither spelling, in
// which case the pre-installed dependencies below are the safeguard.
func setOnDemandRepository(ctx context.Context, sqlDB *sql.DB, repo string) {
	for _, stmt := range buildRepositoryStatements(repo) {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			fmt.Printf("[duckdb] ignored: %s (%v)\n", stmt, err)
			continue
		}
		fmt.Printf("[duckdb] applied: %s\n", stmt)
	}
}

func downloadExtensions() {
	ctx := context.Background()

	sqlDB, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		panic(err)
	}
	defer sqlDB.Close()

	repo := strings.TrimSpace(os.Getenv(EnvDuckDBExtensionRepository))
	if repo != "" {
		fmt.Printf("[duckdb] extension repository: %s\n", repo)
		setOnDemandRepository(ctx, sqlDB, repo)
		for _, dep := range duckdbExtensionDependencies {
			installExtension(ctx, sqlDB, dep, repo, false)
		}
	}

	for _, ext := range duckdbExtensions {
		installExtension(ctx, sqlDB, ext, repo, true)
	}
}

func main() {
	downloadExtensions()
}
