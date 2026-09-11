# Docker 沙箱虚拟机部署与 Helm 挂载改动说明

面向部署方。记录本次「虚拟机提供 Docker 沙箱 + Helm 部署挂载 TLS 证书」的方案设计、
Chart 改动与完整操作手册。后端本身的设计见 [Docker 沙箱后端](./sandbox-docker-backend.md)，
技能（Skill）层配置见 [Agent Skills](./agent-skills.md)。

## 背景与目标

- 部署形态：WeKnora 以 **Helm（Kubernetes）** 部署；Docker 沙箱 daemon 跑在**独立虚拟机**上。
- 目标：让 app Pod 通过 mTLS 连到 VM 的 Docker Engine API（`tcp://<VM-IP>:2376`），
  作为「设置 → 沙箱后端」的 Docker 后端，为 Skill 脚本执行提供宿主。
- 前提结论：无沙箱配置则 Skill 不可用（技能本体只来自沙箱镜像 TenantSkills），
  因此这是启用 Skill 的前置工程。

## 方案选型

| 形态 | 适用 | 关键约束 | 本次是否采用 |
| --- | --- | --- | --- |
| 本机 socket | app 与 daemon 同机（compose 单机） | 需挂 `docker.sock`（等同宿主机 root） | 否（K8s 下 Pod 与 daemon 不同机） |
| **远程 daemon（mTLS）** | **沙箱负载与应用分离** | tcp 远程**强制 TLS**；2376 不得暴露公网 | **是** |
| Cube / E2B | 多机调度 / 内核级隔离 / 内存态快照 | 需要对应集群或云账号 | 备选（未来扩容再看） |

「远程 tcp 不带 TLS」不被支持：`ValidateDockerRemoteTLS` 要求 tcp 地址必须同时给出
TLS 证书目录，明文 2376 保存配置时就会被拒绝。

## Chart 改动（本仓库，3 处，向后兼容）

原 `helm/templates/app.yaml` 的卷是硬编码的（仅 `data-files`），`values.yaml` 没有额外
卷的入口。本次为 app 组件增加通用的 `extraVolumes` / `extraVolumeMounts` 支持，
三处改动逐一列出（改前 → 改后）。

### 改动 1：`helm/templates/app.yaml` — 容器 volumeMounts 追加渲染块

改前：

```yaml
          volumeMounts:
            - name: data-files
              mountPath: /data/files
```

改后（在 `data-files` 之后追加两块）：

```yaml
          volumeMounts:
            - name: data-files
              mountPath: /data/files
            {{- with .Values.app.extraVolumeMounts }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
```

### 改动 2：`helm/templates/app.yaml` — Pod volumes 追加渲染块

改前（`volumes:` 列表只有 `data-files` 一项）：

```yaml
      volumes:
        - name: data-files
          {{- if .Values.dataFiles.persistence.enabled }}
          persistentVolumeClaim:
            claimName: {{ .Values.dataFiles.persistence.existingClaim | default (printf "%s-data-files" (include "weknora.fullname" .)) }}
          {{- else }}
          emptyDir: {}
          {{- end }}
```

改后（列表末尾追加 Pod 级声明；注意缩进是 8 空格，与列表项同级）：

```yaml
      volumes:
        - name: data-files
          {{- if .Values.dataFiles.persistence.enabled }}
          persistentVolumeClaim:
            claimName: {{ .Values.dataFiles.persistence.existingClaim | default (printf "%s-data-files" (include "weknora.fullname" .)) }}
          {{- else }}
          emptyDir: {}
          {{- end }}
      {{- with .Values.app.extraVolumes }}
      {{- toYaml . | nindent 8 }}
      {{- end }}
```

### 改动 3：`helm/values.yaml` — app 段新增两个配置项

改前（`extraEnv` 之后直接进入 `service:` 段）：

```yaml
  # -- Additional environment variables
  extraEnv: []
  # - name: OLLAMA_BASE_URL
  #   value: "http://ollama:11434"
```

改后（`extraEnv` 之后插入两段定义 + 注释示例）：

```yaml
  # -- Additional environment variables
  extraEnv: []
  # - name: OLLAMA_BASE_URL
  #   value: "http://ollama:11434"

  # -- Additional volumes for the app pod (e.g. Docker remote daemon TLS certs)
  extraVolumes: []
  # - name: docker-tls
  #   secret:
  #     secretName: weknora-docker-tls
  #     defaultMode: 0444

  # -- Additional volumeMounts for the app container
  extraVolumeMounts: []
  # - name: docker-tls
  #   mountPath: /etc/weknora/docker-tls
  #   readOnly: true
```

设计要点：

- **三处缺一不可**：只加 volumeMounts 会因卷未声明导致 Pod 起不来（`volume "x" not declared
  in pod spec`）；只加 volumes 则文件挂不进容器；values 是两者的数据源。
- **空值不渲染**：两个 `{{- with }}` 块在默认 `[]` 下输出为空，现有部署行为完全不变。
- 选择 Secret 而非 ConfigMap / hostPath：
  - `key.pem` 是私钥，Secret 卷由 kubelet 挂 **tmpfs**，不落节点盘；
  - hostPath 要求证书放在节点上，但 app Pod 会被调度到任意节点，证书必须**跟着 Pod 走**。
- 命名风格与社区 chart 惯例一致（argocd / cert-manager 同款字段名），后续挂任何东西都能复用。

## 证书签发（VM 侧一次性操作）

CA、服务端、客户端三套证书；CA 私钥**不加密**（`-aes256` 会在每次签发时索要密码短语，
一次性内部 CA 无必要，用文件权限保护）：

```bash
mkdir -p /opt/docker-tls && cd /opt/docker-tls

# CA
openssl genrsa -out ca-key.pem 4096
openssl req -new -x509 -days 3650 -key ca-key.pem -sha256 \
  -subj "/CN=weknora-sandbox-ca" -out ca.pem

# 服务端证书（SAN 必须含 VM IP，docker 客户端按它校验主机名）
openssl genrsa -out server-key.pem 4096
openssl req -subj "/CN=<VM-IP>" -new -key server-key.pem -out server.csr
echo "subjectAltName = IP:<VM-IP>,IP:127.0.0.1" > extfile.cnf
echo "extendedKeyUsage = serverAuth" >> extfile.cnf
openssl x509 -req -days 3650 -in server.csr -CA ca.pem -CAkey ca-key.pem \
  -CAcreateserial -out server-cert.pem -extfile extfile.cnf

# 客户端证书（放 K8s Secret）
openssl genrsa -out key.pem 4096
openssl req -subj "/CN=weknora-client" -new -key key.pem -out client.csr
echo "extendedKeyUsage = clientAuth" > extfile-client.cnf
openssl x509 -req -days 3650 -in client.csr -CA ca.pem -CAkey ca-key.pem \
  -CAcreateserial -out cert.pem -extfile extfile-client.cnf

chmod 0400 ca-key.pem server-key.pem key.pem
chmod 0444 ca.pem server-cert.pem cert.pem
rm server.csr client.csr extfile.cnf extfile-client.cnf
```

硬约束：

- **SAN 必须与后端配置地址严格一致**：配置填 `tcp://<VM-IP>:2376`，SAN 就得含该 IP；
  用域名则 SAN 连域名。对不上报 `certificate is valid for ... not <IP>`。
- 客户端证书必须带 `extendedKeyUsage = clientAuth`，服务端必须 `serverAuth`，
  用错方向报 `tls: failed to verify certificate`。
- 有效期 3650 天（内部 CA 一年一换不值得）。

## VM 侧：dockerd 开 TLS 端口

```bash
# 1. 证书就位
cp ca.pem server-cert.pem server-key.pem /etc/docker/
chmod 0400 /etc/docker/server-key.pem

# 2. daemon.json —— 是"合并"不是覆盖；绝不能写 "hosts"（与 systemd -H fd:// 冲突，
#    dockerd 起不来报 conflicts with command line）
# {
#   ...已有配置...,
#   "tlsverify": true,
#   "tlscacert": "/etc/docker/ca.pem",
#   "tlscert": "/etc/docker/server-cert.pem",
#   "tlskey": "/etc/docker/server-key.pem"
# }

# 3. 先看原文（--containerd 参数要保留）
systemctl cat docker.service | grep ExecStart

# 4. drop-in：ExecStart 是追加语义，必须先写空 ExecStart= 清空再写全量新值
mkdir -p /etc/systemd/system/docker.service.d
cat > /etc/systemd/system/docker.service.d/override.conf <<'EOF'
[Service]
ExecStart=
ExecStart=/usr/bin/dockerd -H fd:// --containerd=/run/containerd/containerd.sock -H tcp://0.0.0.0:2376
EOF

systemctl daemon-reload && systemctl restart docker
ss -tlnp | grep 2376        # 确认监听
```

本机 `docker ps` 不受影响（unix socket 不走 TLS）。

**防火墙只放行 WeKnora 集群出口 IP，严禁 2376 对公网开放**（等同 daemon root）：

```bash
# Ubuntu/Debian
ufw allow from <集群IP> to any port 2376 proto tcp
# CentOS/RHEL
firewall-cmd --permanent --add-rich-rule='rule family=ipv4 source address=<集群IP> port port=2376 protocol=tcp accept'
firewall-cmd --reload
```

## K8s 侧：Secret 创建与 values

```bash
kubectl -n <ns> create secret generic weknora-docker-tls \
  --from-file=ca.pem --from-file=cert.pem --from-file=key.pem
```

`my-values.yaml`：

```yaml
app:
  extraVolumes:
    - name: docker-tls
      secret:
        secretName: weknora-docker-tls
        defaultMode: 0444      # 必须放宽，见下
  extraVolumeMounts:
    - name: docker-tls
      mountPath: /etc/weknora/docker-tls
      readOnly: true
```

`defaultMode: 0444` 的原因：Secret 卷文件属主是 root，而 WeKnora 镜像入口
（`scripts/docker-entrypoint.sh`）会 gosu 降权到 uid 1000 的 `appuser` 运行；
0400 会让 app 进程读证书报 permission denied。

```bash
helm upgrade <release> ./helm -n <ns> -f my-values.yaml --reuse-values
```

## WeKnora 配置与验证链路

1. **打开 Docker 沙箱开关**（默认关闭）：系统管理员在「设置 → 系统设置 → 网络安全」打开
   （DB 优先，立即生效，推荐）；或 `app.env.WEKNORA_SANDBOX_DOCKER_ENABLED: "true"`。
2. Pod 内确认证书：

   ```bash
   kubectl -n <ns> exec deploy/<release>-app -- ls -l /etc/weknora/docker-tls
   ```

   镜像内无 curl，TLS 握手直接走下一步的界面连接检查。
3. **「设置 → 沙箱后端」→ Docker**：

   | 字段 | 值 |
   | --- | --- |
   | 镜像 | `wechatopenai/weknora-sandbox:main`（勿用 latest，见后端文档） |
   | Docker 守护进程地址 | `tcp://<VM-IP>:2376` |
   | TLS 证书目录 | `/etc/weknora/docker-tls` |
   | 允许访问私网集群地址 | 打开 |
   | 网络模式 | `bridge`（默认）或 `none` |
4. 保存即连接检查；通过后按 Skill 链路继续：装技能到该配置 → agent 编辑页绑定
   `sandbox_config_id` → 跑脚本验证会话级持久（同一会话第二次 `shell_exec` 能读到
   第一次写的文件）。

## 运维注意

| 事项 | 说明 |
| --- | --- |
| 证书轮换 | `kubectl create secret` 重建同名 Secret 后，kubelet 自动同步进 Pod（约 1 分钟）；保险起见 `kubectl rollout restart deploy/<release>-app` |
| 网络策略 | 本 chart 无 NetworkPolicy，Pod 出网默认放行；若集群有全局 egress 限制需放行到 VM:2376 |
| 多副本 | Secret 每 Pod 都挂，天然支持 `replicaCount > 1`；会话沙箱绑定依赖 Redis（chart 必装，满足） |
| 快照归属 | Skill 快照是 VM daemon 上的**本地镜像**（`weknora-skill/*`），Pod 重建/漂移不影响——快照不在集群存储里，这是该方案对 K8s 友好的关键 |
| 资源规划 | 单沙箱默认 2 核 / 2048 MB / 512 进程，按并发会话数估算 VM 规格 |
| 镜像层上限 | Docker 增量快照 127 层封顶，技能装/卸只增不减，长期使用关注 VM 磁盘 |
| 空闲回收 | 默认 1800 秒无活动回收容器，可在配置里调 |

## 故障排查

| 报错关键词 | 原因与处置 |
| --- | --- |
| `conflicts with command line` | daemon.json 写了 `hosts`，删掉，监听地址走 drop-in |
| `cannot load ... permission denied` | VM 上 key 文件权限/属主不对（应 0400 且 dockerd 以 root 跑）；或 Pod 内 0400 导致 appuser 读不了（`defaultMode: 0444`） |
| `certificate is valid for ... not <IP>` | 证书 SAN 缺该 IP，重新签 |
| `tls: failed to verify certificate` | 客户端/服务端证书用反了（`cert.pem` 必须是 clientAuth 那张） |
| Pod 起不来 `volume not declared` | 只改了 volumeMounts 没加 volumes 声明，两处要同时有 |
| 配置保存报 TLS 目录无效 | `mountPath` 与「TLS 证书目录」填的路径不一致，以 Pod 内路径为准 |
