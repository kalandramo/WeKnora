# Wiki 与知识图谱数据库表与 SQL 操作指南

> 适用场景：需要直接操作数据库排查或修复 Wiki / 知识图谱数据时使用。
> 生成日期：2026-09-07。表结构以仓库内 `migrations/versioned/` 迁移文件为准，字段定义以 `internal/types/` 为准。
>
> ⚠️ 直接改库会绕过应用层的校验、缓存同步与版本快照机制，优先走 REST API；改库前请备份。

---

## 目录

1. [Wiki 功能的数据库表](#1-wiki-功能的数据库表)
2. [wiki_pages 关键字段说明](#2-wiki_pages-关键字段说明)
3. [Wiki 常用 SQL](#3-wiki-常用-sql)
4. [直接改表的注意事项](#4-直接改表的注意事项)
5. [知识图谱功能的存储](#5-知识图谱功能的存储)
6. [知识图谱 SQL 与 Cypher 操作](#6-知识图谱-sql-与-cypher-操作)
7. [附录：迁移文件与源码索引](#7-附录迁移文件与源码索引)

---

## 1. Wiki 功能的数据库表

Wiki 功能的数据全部落在 **PostgreSQL 主库**（SQLite Lite 模式下有对应 `migrations/sqlite/` 版本）。

| 表 / 列 | 引入迁移 | 作用 |
| --- | --- | --- |
| `wiki_pages` | 000037（建表）/ 000061（层级字段）/ 000075（溯源字段） | 页面主表，**只存当前版本**的内容 |
| `wiki_folders` | 000037 / 000061 | 目录树，邻接表模型（`parent_id = ''` 表示根） |
| `wiki_page_revisions` | 000075 | 历史版本快照，`(page_id, version)` 唯一 |
| `wiki_page_issues` | 000037 | 页面问题（lint / Agent `wiki_flag_issue` 上报） |
| `knowledge_bases.wiki_config` | 000037（新增列） | Wiki 配置 JSONB 列 |
| `knowledge_bases.indexing_strategy` | 000037（新增列） | 索引策略 JSONB 列，含 `wiki_enabled` 开关 |
| `task_pending_ops` | 000041 相关 | 待处理任务持久化（启动恢复用） |

已移除的表：

- `wiki_log_entries` —— 在 `000077_remove_wiki_log` 中整体 DROP，历史遗留 `page_type = 'log'` 页面一并删除。操作历史统一走知识库活动流（审计日志）。

### 页面类型（`page_type`）与状态（`status`）

| 值 | 说明 |
| --- | --- |
| `summary` | 单篇源文档摘要页，slug 形如 `summary/<knowledge-uuid>` |
| `entity` | 实体页（人、组织、产品、技术等） |
| `concept` | 概念/主题页 |
| `index` | wiki 级索引页 |
| `synthesis` / `comparison` | 综合分析页 / 对比页，**仅 Agent 的 `wiki_write_page` 工具可创建** |
| `draft` / `published` / `archived` | 状态：草稿 / 已发布（默认）/ 归档 |

---

## 2. wiki_pages 关键字段说明

完整定义见 `internal/types/wiki_page.go`（`WikiPage` 结构体）。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | varchar(36) | 页面 UUID，主键 |
| `tenant_id` | bigint | 租户隔离 |
| `knowledge_base_id` | varchar(36) | 所属知识库 |
| `slug` | varchar(255) | KB 内唯一标识，格式 `<type>/<name>`；部分唯一索引 `(knowledge_base_id, slug) WHERE deleted_at IS NULL` |
| `title` / `summary` / `content` | varchar/text | 标题 / 一行摘要 / Markdown 正文 |
| `page_type` / `status` | varchar(32) | 见上表 |
| `aliases` | JSONB | 别名数组（搜索与去重合并后的旧名指向） |
| `folder_id` | varchar(36) | 页面目录归属的**唯一事实来源**（`wiki_folders.id`，`''` 为根） |
| `category_path` / `wiki_path` / `depth` / `sort_order` | JSONB/varchar/int | folder 链的**缓存投影**，写路径自动重算 |
| `source_refs` | JSONB | 来源文档引用，元素格式 `"<knowledge_id>|<doc_title>"` |
| `chunk_refs` | JSONB | 分块级证据 chunk UUID 列表 |
| `in_links` / `out_links` | JSONB | wiki-link 反向/正向链接（slug 数组），`GET /graph` 的数据源 |
| `version` | int | 版本号，仅用户可见内容变化时递增（也是 API 乐观锁依据） |
| `last_edit_source` | varchar(16) | 当前版本作者：`pipeline` / `agent` / `user` / `revert`（空串按 pipeline 处理） |
| `last_editor_id` | varchar(64) | 当前版本操作者（管道写入为空） |
| `created_at` / `updated_at` / `deleted_at` | timestamptz | 时间戳；`deleted_at` 非空即软删除 |

`wiki_config`（`knowledge_bases.wiki_config` JSONB）常用键：

| 键 | 默认 | 说明 |
| --- | --- | --- |
| `synthesis_model_id` | — | 生成页面用的 LLM 模型 ID |
| `extraction_granularity` | `standard` | 抽取粒度：`focused` / `standard` / `exhaustive` |
| `max_pages_per_ingest` | 0（不限） | 单次 ingest 建页上限 |
| `ingest_batch_size` / `ingest_map_parallel` / `ingest_reduce_parallel` / `ingest_max_inflight` | 5 / 10 / 10 / 4 | 并发批处理参数 |

---

## 3. Wiki 常用 SQL

```sql
-- 1. 查看某知识库的全部页面
SELECT slug, title, page_type, status, version, last_edit_source, updated_at
FROM wiki_pages
WHERE knowledge_base_id = '<kb_id>' AND deleted_at IS NULL
ORDER BY updated_at DESC;

-- 2. 按类型/状态统计
SELECT page_type, status, count(*) 
FROM wiki_pages
WHERE knowledge_base_id = '<kb_id>' AND deleted_at IS NULL
GROUP BY page_type, status;

-- 3. 手工修正页面内容
--    注意：不会写入 wiki_page_revisions 快照，版本历史会缺一环；
--    也不递增 version。若在意版本链，请走 PUT /wiki/pages/*slug API。
UPDATE wiki_pages
SET content = '新内容', updated_at = NOW()
WHERE knowledge_base_id = '<kb_id>' AND slug = 'entity/xxx' AND deleted_at IS NULL;

-- 4. 批量下线页面（保留数据，前端不再展示）
UPDATE wiki_pages
SET status = 'archived'
WHERE knowledge_base_id = '<kb_id>' AND page_type = 'summary';

-- 5. 软删除页面
UPDATE wiki_pages
SET deleted_at = NOW()
WHERE knowledge_base_id = '<kb_id>' AND slug = 'entity/xxx';

-- 6. 查看页面问题
SELECT slug, issue_type, status, description, created_at
FROM wiki_page_issues
WHERE knowledge_base_id = '<kb_id>' AND deleted_at IS NULL
ORDER BY created_at DESC;

-- 7. 批量关闭已处理的问题
UPDATE wiki_page_issues
SET status = 'resolved', updated_at = NOW()
WHERE knowledge_base_id = '<kb_id>' AND status = 'pending' AND slug = 'entity/xxx';

-- 8. 查看某页面历史版本
SELECT version, edit_source, editor_id, edited_at, length(content) AS content_len
FROM wiki_page_revisions
WHERE knowledge_base_id = '<kb_id>' AND slug = 'entity/xxx'
ORDER BY version DESC;

-- 9. 修改 Wiki 配置（抽取粒度）
UPDATE knowledge_bases
SET wiki_config = jsonb_set(COALESCE(wiki_config, '{}'::jsonb),
                            '{extraction_granularity}', '"exhaustive"')
WHERE id = '<kb_id>';

-- 10. 开/关 Wiki 功能开关（只改这一个开关，无需动其他键）
UPDATE knowledge_bases
SET indexing_strategy = jsonb_set(COALESCE(indexing_strategy, '{}'::jsonb),
                                  '{wiki_enabled}', 'true')
WHERE id = '<kb_id>';

-- 11. 清掉卡死的待恢复任务（谨慎：仅当确认对应 KB 已无生成需求时）
DELETE FROM task_pending_ops
WHERE scope = 'knowledge_base' AND task_type IN ('wiki:ingest', 'wiki:finalize');
```

**Wiki 开关的生效链路**：`indexing_strategy.wiki_enabled = true` 后，文档摄入管道会在解析完成时为该 KB 入队 `wiki:ingest` 任务（Redis 队列，异步执行）。已有文档会被自动纳入，无需重传。

### 3.1 重命名 slug 的操作方案

slug 与版本历史、问题列表、引用页正文/链接均有联动，**不是孤立字段**。直接 `UPDATE wiki_pages.slug` 一列会留下多处不一致（详见下文方案 B 的风险清单）。

#### 方案 A（首选）：让 Agent 重命名

在启用了该 KB 的 Agent 会话里说「把 wiki 页面 `entity/amazon-cloud` 重命名为 `entity/aliyun-cloud`」，走 `wiki_rename_page` 工具（实现见 `internal/agent/tools/wiki_rename_page.go`）。它会做**全量级联**：

1. 以全部字段复制创建新 slug 页面；
2. 重写所有引用页正文中的 `[[旧slug]]` / `[[旧slug|` 为新 slug（`applyIncomingWikiContentRewrite`）；
3. 删除旧 slug 页面；
4. `InjectCrossLinks` 注入交叉链接、`RebuildIndexPage` 重建索引页缓存。

任一步失败会整体回滚（回滚内容改写 + 清理新建页）。本次重命名在版本历史中以 `edit_source = agent` 记录，可追溯、可回滚。

#### 方案 B：直接 SQL（需自行处理全部联动）

| # | 风险 | 原因 |
| --- | --- | --- |
| 1 | 唯一索引冲突 | `(knowledge_base_id, slug) WHERE deleted_at IS NULL`，改前需确认目标 slug 未被占用 |
| 2 | 引用页正文死链 | 其他页面正文里的 `[[旧slug]]` / `[[旧slug|` 不会自动更新 |
| 3 | 引用页 `out_links` 残留旧值 | 图结构（`GET /graph`）与 lint 报死链 |
| 4 | 版本历史「丢失」 | `wiki_page_revisions` 表有自己的 `slug` 列，`GET /revisions/*slug` 按 slug 查——不同步则旧版本按新 slug 查不到 |
| 5 | 问题列表错位 | `wiki_page_issues` 同样带 `slug` 列，按旧 slug 关联的 issue 会跟丢 |
| 6 | 索引页缓存旧链接 | `page_type = index` 页面可能缓存了旧 slug 的交叉链接 |

完整脚本（示例：`entity/amazon-cloud` → `entity/aliyun-cloud`）：

```sql
BEGIN;

-- 1. 改页面 slug
UPDATE wiki_pages
SET slug = 'entity/aliyun-cloud'
WHERE id = '<page_id>' AND knowledge_base_id = '<kb_id>' AND deleted_at IS NULL;

-- 2. 同步版本历史的 slug（否则历史按新 slug 查不到）
UPDATE wiki_page_revisions
SET slug = 'entity/aliyun-cloud'
WHERE knowledge_base_id = '<kb_id>' AND slug = 'entity/amazon-cloud';

-- 3. 同步问题表的 slug
UPDATE wiki_page_issues
SET slug = 'entity/aliyun-cloud', updated_at = NOW()
WHERE knowledge_base_id = '<kb_id>' AND slug = 'entity/amazon-cloud' AND deleted_at IS NULL;

-- 4. 修引用页的 out_links（带闭合引号防前缀误伤，如 entity/amazon-cloud-overview）
UPDATE wiki_pages
SET out_links = replace(out_links::text, '"entity/amazon-cloud"', '"entity/aliyun-cloud"')::jsonb
WHERE knowledge_base_id = '<kb_id>'
  AND out_links @> '["entity/amazon-cloud"]'::jsonb AND deleted_at IS NULL;

-- 5. 修全 KB 正文里的 wiki 链接（两种形式）
UPDATE wiki_pages
SET content = replace(replace(content,
      '[[entity/amazon-cloud]]', '[[entity/aliyun-cloud]]'),
      '[[entity/amazon-cloud|',  '[[entity/aliyun-cloud|')
WHERE knowledge_base_id = '<kb_id>'
  AND content LIKE '%entity/amazon-cloud%' AND deleted_at IS NULL;

COMMIT;
```

改完后调 `POST /api/v1/knowledgebase/:kb_id/wiki/rebuild-links` 重建链接图并刷新索引页缓存，兜底消除残留不一致。

**不用动的字段**：`in_links`（存的是引用方页面的 slug）；`wiki_path`（由 page_type + category_path + title 派生，不含 slug）；`source_refs` / `chunk_refs`（按 knowledge_id 引用）。

---

## 4. 直接改表的注意事项

1. **目录树缓存投影不会自动同步**：`folder_id` 是唯一事实来源，但 `category_path` / `wiki_path` / `depth` 是从 folder 链派生的缓存。直接 UPDATE `folder_id` 会导致浏览器目录树错位。移动页面请走 `PUT /api/v1/knowledgebase/:kb_id/wiki/move-page`。
2. **链接图需手动重建**：`in_links` / `out_links` 维护页面间链接。手动增删页面或改正文后，调 `POST /api/v1/knowledgebase/:kb_id/wiki/rebuild-links` 重建，否则 `GET /graph` 与 lint 结果不一致。
3. **版本快照缺失**：直接 UPDATE `wiki_pages` 不经过「先快照后更新」写路径，`wiki_page_revisions` 不会有记录，版本历史出现断层。
4. **slug 唯一性**：改 slug 前先确认目标 slug 未被占用（含软删除行？不含——部分索引只约束 `deleted_at IS NULL`，但为避免混乱建议避开）；改 slug 后旧的反向链接不会自动更新。重命名 slug 是多表联动操作（`wiki_pages` / `wiki_page_revisions` / `wiki_page_issues` / 引用页正文与 out_links），完整方案见 3.1 节。
5. **JSONB 列用函数更新**：`wiki_config` / `indexing_strategy` / `aliases` 等 JSONB 列，优先用 `jsonb_set()` 做点更新，避免整列覆盖时丢掉其他键。
6. **软删除约定**：查询一律带 `deleted_at IS NULL`；恢复误删页面直接把 `deleted_at` 置 NULL 即可（前提是 slug 未被新页面占用）。

---

## 5. 知识图谱功能的存储

知识图谱的数据 **不在 PostgreSQL**，图数据存在 **Neo4j**（唯一图存储后端，依赖 APOC 插件）：

| 存储 | 内容 |
| --- | --- |
| Neo4j | 实体节点与关系。节点标签为命名空间映射：`ENTITY<kb_id>`、`ENTITY<knowledge_id>`（连字符替换为下划线）；节点属性 `name`、`kg`（knowledge_id）、`attributes`、`chunks`（来源 chunk UUID 列表） |
| PostgreSQL | 仅配置，无图数据 |

PostgreSQL 侧的两个配置列（都在 `knowledge_bases` 表）：

| 列 | 引入迁移 | 作用 |
| --- | --- | --- |
| `indexing_strategy` | 000037 | `graph_enabled` 开关 |
| `extract_config` | 000000 初始建表即有 | 抽取配置：`enabled`、few-shot 示例（`text` / `tags` / `nodes` / `relations`）、`custom_instructions` |

**图谱生效的三个条件（缺一不可）**：

1. 环境变量 `NEO4J_ENABLE=true`（全局开关，`initNeo4jClient` 启动时校验连接）；
2. `indexing_strategy.graph_enabled = true`；
3. `extract_config.enabled = true`。

判断逻辑见 `internal/types/knowledgebase.go` 的 `IsGraphEnabled()`。另外读取路径上存在 legacy 同步：`ExtractConfig.Enabled = true` 时会单向把 `IndexingStrategy.GraphEnabled` 置 true。

---

## 6. 知识图谱 SQL 与 Cypher 操作

### 6.1 PostgreSQL 配置操作

```sql
-- 1. 开启某 KB 的图谱功能（两个开关都要开）
UPDATE knowledge_bases
SET indexing_strategy = jsonb_set(COALESCE(indexing_strategy, '{}'::jsonb),
                                  '{graph_enabled}', 'true'),
    extract_config = jsonb_set(COALESCE(extract_config, '{"enabled":false}'::jsonb),
                               '{enabled}', 'true')
WHERE id = '<kb_id>';

-- 2. 查看哪些 KB 开了图谱
SELECT id, name,
       indexing_strategy->>'graph_enabled' AS graph_on,
       extract_config->>'enabled'          AS extract_on
FROM knowledge_bases
WHERE deleted_at IS NULL
  AND indexing_strategy->>'graph_enabled' = 'true';

-- 3. 为已有抽取配置追加自定义抽取指令
UPDATE knowledge_bases
SET extract_config = jsonb_set(COALESCE(extract_config, '{}'::jsonb),
                               '{custom_instructions}', '重点关注合同主体与金额条款')
WHERE id = '<kb_id>';

-- 4. 清空已删除 KB 的残留配置
UPDATE knowledge_bases
SET extract_config = NULL
WHERE deleted_at IS NOT NULL;
```

开启后**重新解析文档**即可触发抽取：`knowledge_post_process.go` 在解析完成时逐文本 chunk 入队 `TypeChunkExtract` 任务（独立 asynq `QueueGraph` 队列），逐 chunk 调 LLM 抽取并写 Neo4j。已有文档无需重传。

### 6.2 Neo4j Cypher 操作

标签名规则：`ENTITY` + 标识符中的连字符 `-` 替换为下划线 `_`。例如 `kb_id = a1b2c3d4-1234-...` 对应标签 `ENTITY_a1b2c3d4_1234_...`。

```cypher
-- 1. 查看某 KB 的全部实体（先在 Neo4j 里用 CALL db.labels() 确认实际标签名）
MATCH (n:`ENTITY_<kb_id_下划线形式>`)
RETURN n.name, n.kg, n.attributes, size(n.chunks) AS chunk_count
LIMIT 100;

-- 2. 模糊查实体（与检索侧 SearchNode 的 `n.name CONTAINS` 一致）
MATCH (n:`ENTITY_<kb_id_下划线形式>`)
WHERE n.name CONTAINS '关键词'
RETURN n.name, n.chunks;

-- 3. 查某实体的关系（一跳）
MATCH (n:`ENTITY_<kb_id_下划线形式>` {name: '实体名'})-[r]-(m)
RETURN n.name, type(r), m.name;

-- 4. 删除某个文档的图谱数据（等价于后端 DelGraph 按知识级标签删除）
MATCH (n:`ENTITY_<knowledge_id_下划线形式>`)
DETACH DELETE n;

-- 5. 删除整个 KB 的图谱数据
MATCH (n:`ENTITY_<kb_id_下划线形式>`)
DETACH DELETE n;
```

### 6.3 一致性提醒

- 图谱检索召回链路：Neo4j 节点 `chunks` 属性 → 去 PG `chunks` 表拉原文 → 并入检索候选集。**手工删改 Neo4j 数据不影响 PG 侧 chunk**，两边不同步会导致召回到已失效 chunk 的引用；
- 删除文档/知识库时后端会自动调 `DelGraph` 清图；手工清残留时注意标签两级结构（KB 级与 knowledge 级）；
- `GET /wiki/graph` 是 **Wiki 功能自己的页面链接图**（数据源 `wiki_pages.in_links/out_links`），与本文的知识图谱（Neo4j 实体关系）**无关**，排查时勿混淆。

---

## 7. 附录：迁移文件与源码索引

### 迁移文件（`migrations/versioned/`）

| 文件 | 内容 |
| --- | --- |
| `000037_wiki_and_indexing.up.sql` | `wiki_pages` / `wiki_folders` / `wiki_page_issues` 建表；`knowledge_bases.wiki_config`、`indexing_strategy` 列 |
| `000061_wiki_page_hierarchy.up.sql` | `wiki_pages` 层级字段（`folder_id` / `category_path` / `wiki_path` / `depth` / `sort_order`） |
| `000075_wiki_page_revisions.up.sql` | `wiki_page_revisions` 表；`wiki_pages.last_edit_source` / `last_editor_id` 列 |
| `000040_wiki_log_entries.up.sql` | （已被 000077 移除）旧操作日志表 |
| `000077_remove_wiki_log.up.sql` | DROP `wiki_log_entries`，删除 `page_type = 'log'` 页面 |
| `000041_task_queue_and_wiki_indexes.up.sql` | 任务队列与 wiki 相关索引（含 `task_pending_ops`） |

### 源码（相对仓库根目录）

| 层 | 文件 |
| --- | --- |
| Wiki 数据结构 | `internal/types/wiki_page.go` |
| 知识库配置结构 | `internal/types/knowledgebase.go`（`ExtractConfig` / `IndexingStrategy` / `IsGraphEnabled`） |
| 索引策略结构 | `internal/types/indexing_strategy.go` |
| Wiki HTTP Handler | `internal/handler/wiki_page.go` |
| Wiki 生成管道 | `internal/application/service/wiki_ingest*.go` |
| 图谱抽取任务 | `internal/application/service/extract.go`、`knowledge_post_process.go` |
| Neo4j 仓储 | `internal/application/repository/retriever/neo4j/repository.go` |
| 失败恢复 | `internal/container/recover_pending_wiki_tasks.go` |

### 相关文档

- `website-docs/03-features/14-wiki.md` —— Wiki 功能全貌
- `website-docs/03-features/09-knowledge-graph.md` —— 知识图谱功能全貌
- `docs/开启知识图谱功能.md`、`docs/KnowledgeGraph.md` —— 图谱开启与配置
