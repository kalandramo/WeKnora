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

func downloadExtensions() {
	ctx := context.Background()

	sqlDB, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		panic(err)
	}
	defer sqlDB.Close()

	repo := strings.TrimSpace(os.Getenv(EnvDuckDBExtensionRepository))

	for _, ext := range duckdbExtensions {
		// 使用 INSTALL <ext> FROM '<repo>' 语法而非 SET
		// custom_extension_repository：前者是 DuckDB 官方推荐的自定义仓库
		// 写法，且不依赖配置项名称，跨版本更稳。
		stmt := fmt.Sprintf("INSTALL %s;", ext)
		if repo != "" {
			// 单引号转义，避免仓库地址含特殊字符时拼坏 SQL。
			stmt = fmt.Sprintf("INSTALL %s FROM '%s';", ext, strings.ReplaceAll(repo, "'", "''"))
		}
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			panic(fmt.Errorf("failed to install %s extension: %w", ext, err))
		}
		if _, err := sqlDB.ExecContext(ctx, fmt.Sprintf("LOAD %s;", ext)); err != nil {
			panic(fmt.Errorf("failed to load %s extension: %w", ext, err))
		}
	}
}

func main() {
	downloadExtensions()
}
