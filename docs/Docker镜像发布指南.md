# WeKnora Docker 镜像 CI 发布指南

> 适用对象：负责把 WeKnora 构建成 Docker 镜像并发布到镜像仓库的同学。
> 最后更新：2026-09-04

## 1. 目标

- 只在**打 `v*` tag** 时自动构建并推送全部镜像（已在 `docker-image.yml` 移除了 `push main` 自动触发）。
- 镜像统一推送到**阿里云容器镜像服务 ACR（深圳地域）**，不再推 Docker Hub。

## 2. 流水线文件

| 文件 | 作用 | 状态 |
|---|---|---|
| `.github/workflows/docker-image.yml` | 构建并推送 5 类镜像到 ACR | **启用（仅 tag 触发）** |
| 其余 11 个 workflow | 测试 / lint / 发布 npm、PyPI、原生二进制 | **在 GitHub UI 逐个 Disable** |

> 仓库其他流水线（`anydoc` / `app` / `cli` / `cli-e2e` / `docreader` / `dsh-plugin` / `frontend` / `go-lint` / `go-lint-cache` / `mcp-server` / `release-lite`）与镜像构建无关，若只关心镜像发布，可在 GitHub 仓库 **Settings → Actions → 选 workflow → `...` → Disable workflow** 关闭。

## 3. 产物镜像

流水线构建并推送以下镜像到 `registry.cn-shenzhen.aliyuncs.com/<ACR_NAMESPACE>/`：

| 镜像 | 内容 | 架构 |
|---|---|---|
| `weknora-app` | Go 主后端（含 anydoc 文档解析） | amd64 + arm64（原生 runner 分别构建后合并 manifest） |
| `weknora-ui` | 前端 | amd64 + arm64（QEMU） |
| `weknora-docreader` | 文档解析服务 | amd64 + arm64（QEMU） |
| `weknora-sandbox` | 代码沙箱 | amd64 + arm64（QEMU） |
| `weknora-sandbox-cube` | 带 envd 的 Cube 变体 | amd64 only |

标签规则（`docker/metadata-action`）：打 `v1.2.3` tag 会自动生成 `v1.2.3` / `v1.2` / `v1` / `latest` 四个标签。

## 4. 需要的 GitHub Secrets

在仓库 **Settings → Secrets and variables → Actions** 中配置：

| Secret | 说明 |
|---|---|
| `ACR_NAMESPACE` | 阿里云 ACR 命名空间（控制台「命名空间」中创建，如 `wechatopenai`） |
| `ACR_USERNAME` | ACR 登录用户名（阿里云主账号登录名或 RAM 子账号） |
| `ACR_PASSWORD` | ACR **访问凭证密码**（控制台「访问凭证」设置，非阿里云账号登录密码） |

> 原 `DOCKERHUB_USERNAME` / `DOCKERHUB_PASSWORD` 已不再使用，可保留也可删除。

## 5. 阿里云 ACR 前置准备

1. 进入**容器镜像服务 ACR 控制台 → 深圳地域**。
2. 确认「命名空间」已存在（与 `ACR_NAMESPACE` 一致）。
3. 建议提前创建仓库：`weknora-app` / `weknora-ui` / `weknora-docreader` / `weknora-sandbox`（个人版若开启「自动创建仓库」可省略）。
4. 在「访问凭证」中设置固定密码，填入 `ACR_PASSWORD`。

## 6. 发布操作步骤

```bash
# 在项目根目录
git tag v0.8.0
git push origin v0.8.0
```

推送后 GitHub Actions 自动执行：

1. `build-ui` / `build-docreader` / `build-sandbox`（含 cube）分别构建并推送；
2. `build-app` 用 matrix 在 `ubuntu-latest`(amd64) 与 `ubuntu-24.04-arm`(arm64) 原生编译，按 digest 推送；
3. `merge` job 把两个架构 digest 合并成多架构 manifest，并打上 `v0.8.0` / `v0.8` / `v0` / `latest` 标签。

最终镜像地址示例：

```
registry.cn-shenzhen.aliyuncs.com/<ACR_NAMESPACE>/weknora-app:v0.8.0
registry.cn-shenzhen.aliyuncs.com/<ACR_NAMESPACE>/weknora-ui:latest
```

## 7. 本地构建（内网环境注意）

CI Runner 可正常访问公网源；但**本地在内网构建**时，构建容器内访问 `deb.debian.org` 的 apt 源会被网关拦截（`Clearsigned file isn't valid, got 'NOSPLIT'`）。

构建命令（与 CI 单架构一致，用 `env` 模式提取版本，避免空格分词）：

```bash
eval "$(./scripts/get_version.sh env)"
docker buildx build -f docker/Dockerfile.app -t weknora-app:local \
  --build-arg "VERSION_ARG=$VERSION" \
  --build-arg "COMMIT_ID_ARG=$COMMIT_ID" \
  --build-arg "BUILD_TIME_ARG=$BUILD_TIME" \
  --build-arg "GO_VERSION_ARG=$GO_VERSION" \
  --build-arg "WITH_ANYDOC=1" \
  --build-arg "APK_MIRROR_ARG=<内网或国内 Debian 源 host，如 mirrors.cloud.tencent.com>" \
  --build-arg "GOPROXY_ARG=https://goproxy.cn,direct" \
  .
```

- `APK_MIRROR_ARG` 只传 host（不含 `/debian`），Dockerfile 会用 `sed` 替换 `deb.debian.org` 并保留原 `/debian` 后缀。
- `WITH_ANYDOC=1` 会编译 Rust anydoc 库（office 文档解析）；不需要可设 `0` 加速。
- 若内网需代理出公网，建议给 `Dockerfile.app` 增加 `ARG http_proxy/https_proxy` + `ENV`（默认空，不影响 CI）。

## 8. 备注

- 地域写死在 `docker-image.yml` 顶部 `env.REGISTRY = registry.cn-shenzhen.aliyuncs.com`；换地域（如杭州）改此处即可。
- 若使用 ACR 企业版，registry 域名为 `xxx-registry.cn-shenzhen.cr.aliyuncs.com`，需同步修改 `REGISTRY`。

## 9. 仅 watsons 分支的 tag 才构建

`docker-image.yml` 通过 `tags:` 过滤只能匹配 tag 名称，**无法按分支过滤**。因此额外增加 `check-branch` job 作为闸门：

- 构建前检查打 tag 的 commit 是否属于 `watsons` 分支（`git merge-base --is-ancestor`）；
- 输出 `build=true/false`，所有 `build-*` 与 `merge` job 均 `needs: check-branch` 且 `if: needs.check-branch.outputs.build == 'true'`；
- 不在 `watsons` 分支打的 tag（如基于 main）会被整体跳过，且不报错。

> 前置：仓库需存在 `watsons` 分支，否则 `git fetch origin watsons` 会失败。
> 若需改为限定其它分支，把 `check-branch` job 里的分支名 `watsons` 替换即可。
