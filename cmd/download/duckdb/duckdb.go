package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

// EnvDuckDBExtensionRepository 指定 DuckDB 扩展仓库的根地址（可选）。
//
// 内网只允许 HTTPS 出网时，DuckDB 编译期默认的 http://extensions.duckdb.org
// 会被准入网关拦成 HTML 门户页，INSTALL 拿到非 gzip 内容后报
// "Input is not a GZIP stream"。把本变量设为 HTTPS 地址即可绕开。
//
// 但仅把协议改成 HTTPS 还不够：1.5.x 会把配置值当成基地址，一律在后面拼
// {version}/{platform}/{name}.duckdb_extension（**不带 .gz**），而官方仓库
// 只提供 .gz，于是 404。该行为无配置项可改，所以本工具不再依赖 INSTALL，
// 而是自己下载 .gz 解压到扩展目录（见 downloadExtensions）。
//
// 留空则保持上游行为（直接 INSTALL，走编译期默认仓库）。
const EnvDuckDBExtensionRepository = "DUCKDB_EXTENSION_REPOSITORY"

// duckdbExtensions 是 WeKnora 数据分析工具实际用到的扩展。
// spatial 提供 st_read_meta（枚举 Excel 里的 sheet 名），excel 提供 read_xlsx。
var duckdbExtensions = []string{"spatial", "excel"}

// duckdbExtensionDependencies 是 DuckDB 在加载上面两项时会按需拉取的依赖。
// spatial 依赖 httpfs，而按需安装既不走 INSTALL ... FROM，也不受仓库设置项
// 的后缀控制，内网里必然失败。所以一并预置。
var duckdbExtensionDependencies = []string{"httpfs"}

// downloadQueue 按依赖顺序返回需要预置的扩展：先装依赖，再装用到它们的主体。
func downloadQueue() []string {
	queue := make([]string, 0, len(duckdbExtensionDependencies)+len(duckdbExtensions))
	queue = append(queue, duckdbExtensionDependencies...)
	return append(queue, duckdbExtensions...)
}

// extensionArchiveURL 拼出扩展压缩包的直链。
//
// 形如 {repo}/{version}/{platform}/{name}.duckdb_extension.gz，与官方仓库的
// 目录结构一致；内网镜像只要按同样的层级放文件即可直接使用。
func extensionArchiveURL(repo, version, platform, ext string) string {
	return fmt.Sprintf("%s/%s/%s/%s.duckdb_extension.gz",
		strings.TrimRight(strings.TrimSpace(repo), "/"), version, platform, ext)
}

// extensionTargetPath 返回 DuckDB 期望找到扩展文件的绝对路径。
//
// 布局为 {extension_directory}/{version}/{platform}/{name}.duckdb_extension，
// 未压缩——这正是 DuckDB 自己 INSTALL 时会写入的位置，因此手动放置后
// LOAD 能直接命中，运行期不再联网。
func extensionTargetPath(extensionDir, version, platform, ext string) string {
	return filepath.Join(extensionDir, version, platform, ext+".duckdb_extension")
}

// duckdbInstallTarget 问 DuckDB 自己要版本、平台和扩展目录，避免写死
// v1.5.2/linux_amd64 这类会随升级失效的字面量。
func duckdbInstallTarget(ctx context.Context, db *sql.DB) (version, platform, dir string, err error) {
	if err = db.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
		return "", "", "", fmt.Errorf("read duckdb version: %w", err)
	}
	if err = db.QueryRowContext(ctx, "PRAGMA platform").Scan(&platform); err != nil {
		return "", "", "", fmt.Errorf("read duckdb platform: %w", err)
	}

	var setting string
	if err = db.QueryRowContext(ctx, "SELECT current_setting('extension_directory')").Scan(&setting); err != nil {
		return "", "", "", fmt.Errorf("read extension_directory: %w", err)
	}
	if strings.TrimSpace(setting) == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", "", "", fmt.Errorf("resolve extension_directory: %w", homeErr)
		}
		setting = filepath.Join(home, ".duckdb", "extensions")
	}
	return version, platform, setting, nil
}

// fetchAndExtract 下载 gz 并解压到 dst。先写 .part 再改名，避免中断时留下
// 半个文件被 DuckDB 当成有效扩展加载。
func fetchAndExtract(ctx context.Context, client *http.Client, srcURL, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srcURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 把响应体开头带进错误里：内网被拦截时这里通常是 HTML 门户页，
		// 有内容才看得出是 404、认证页还是别的。
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("GET %s: HTTP %d: %s", srcURL, resp.StatusCode,
			strings.TrimSpace(string(snippet)))
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("GET %s: %w", srcURL, err)
	}
	defer gz.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, gz); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// installExtension 保留上游路径：未配置仓库时按老样子 INSTALL，行为和
// 上游完全一致。
func installExtension(ctx context.Context, db *sql.DB, ext string) error {
	if _, err := db.ExecContext(ctx, "INSTALL "+ext+";"); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, "LOAD "+ext+";")
	return err
}

func downloadExtensions() {
	ctx := context.Background()

	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	repo := strings.TrimSpace(os.Getenv(EnvDuckDBExtensionRepository))
	if repo == "" {
		for _, ext := range duckdbExtensions {
			if err := installExtension(ctx, db, ext); err != nil {
				panic(fmt.Errorf("failed to install %s extension: %w", ext, err))
			}
			fmt.Printf("[duckdb] %s installed and loaded\n", ext)
		}
		return
	}

	fmt.Printf("[duckdb] extension repository: %s\n", repo)

	version, platform, extDir, err := duckdbInstallTarget(ctx, db)
	if err != nil {
		panic(err)
	}
	fmt.Printf("[duckdb] target: %s / %s -> %s\n", version, platform, extDir)

	client := &http.Client{Timeout: 10 * time.Minute}
	for _, ext := range downloadQueue() {
		url := extensionArchiveURL(repo, version, platform, ext)
		dst := extensionTargetPath(extDir, version, platform, ext)
		if err := fetchAndExtract(ctx, client, url, dst); err != nil {
			panic(fmt.Errorf("failed to fetch %s extension: %w", ext, err))
		}
		fmt.Printf("[duckdb] fetched %s -> %s\n", ext, dst)
	}

	// 落盘不等于可用：DuckDB 加载时会校验签名与平台，这里逐个 LOAD 一遍，
	// 让构建期就暴露问题，而不是等到运行期第一次跑 SQL 才发现。
	for _, ext := range downloadQueue() {
		if _, err := db.ExecContext(ctx, "LOAD "+ext+";"); err != nil {
			panic(fmt.Errorf("failed to load %s extension: %w", ext, err))
		}
		fmt.Printf("[duckdb] %s loaded\n", ext)
	}
}

func main() {
	downloadExtensions()
}
