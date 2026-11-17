# 内网部署与 HTTPS

适用于部署在公司服务器上、只给内部使用的情况，服务器通常同时有公网 IP 和内网 IP，没有域名。
`docker-compose.proxy.yml` 在整套服务前放一个 nginx，用内网 IP 提供 HTTPS：

| 端口 | 内容 |
| --- | --- |
| 443 | Multica。浏览器和守护进程的 WebSocket 由 nginx 直接转给后端 |
| 3443 | Gitea（叠加了 `docker-compose.gitea.yml` 时） |
| 80 | 跳转到 443 |
| 2222 | Gitea SSH，不经过 nginx（可选） |

## 只监听内网地址

Docker 映射端口时会绕过宿主机防火墙（ufw、firewalld 都拦不住），所以端口绑在哪个地址上，决定了它是否暴露到公网：

- nginx 的端口默认绑在 `PROXY_HOST`，也就是内网 IP，公网 IP 上什么都不监听。
- 前端、后端、Gitea 的 HTTP 端口只绑 `127.0.0.1`，只有 nginx 能访问。
- Gitea SSH 默认也只绑 `127.0.0.1`；需要 SSH 克隆时把 `GITEA_SSH_BIND` 设为内网 IP。
- 不要把任何端口改成 `0.0.0.0`。云安全组再额外拦掉公网入站，作为第二道防线。

## 1. 配置

在 `.env` 里写上（以内网 IP `10.0.0.5` 为例）：

```sh
PROXY_HOST=10.0.0.5                     # 内网 IP（或内网主机名，此时另设 PROXY_BIND=10.0.0.5）
FRONTEND_ORIGIN=https://10.0.0.5
MULTICA_PUBLIC_URL=https://10.0.0.5
GITEA_ROOT_URL=https://10.0.0.5:3443/
GITEA_SSH_BIND=10.0.0.5                 # 不需要 SSH 克隆就不写
GITEA_SSH_DOMAIN=10.0.0.5
```

```sh
docker compose -f docker-compose.selfhost.yml -f docker-compose.gitea.yml -f docker-compose.proxy.yml \
  --profile ci up -d
```

宿主机端口可以用 `PROXY_HTTP_PORT`、`PROXY_HTTPS_PORT`、`PROXY_GITEA_PORT` 改；改了 HTTPS 端口，上面的地址里要带上端口。

运行时主机改用新地址：`multica setup self-host --server-url https://10.0.0.5 --app-url https://10.0.0.5`，然后重启守护进程。
运行时主机要能访问服务器的内网 IP；不在同一内网的机器先接入 VPN。

## 2. 证书

证书文件在 `PROXY_CERT_DIR`（默认 `data/proxy-certs/`，不进仓库），`PROXY_TLS` 二选一：

- **`selfsigned`（默认）**：首次启动生成一个私有 CA（`ca.pem`，十年有效），再用它给 `PROXY_HOST` 签发服务器证书。
  客户端信任一次 `ca.pem` 即可；以后换地址只重签服务器证书，不用重新分发。`ca-key.pem` 是 CA 私钥，不要外传。
- **`provided`**：用公司内部 CA 签发的证书。把 `fullchain.pem`（服务器证书在前、中间证书在后）和 `privkey.pem`
  放进证书目录，证书要包含 `PROXY_HOST`（IP 地址写在 SAN 的 IP 项里）。换证书后执行 `docker compose ... restart proxy`。

## 3. 让客户端信任自签 CA

只有 `selfsigned` 需要。把服务器上的 `data/proxy-certs/ca.pem` 拷到客户端：

| 客户端 | 做法 |
| --- | --- |
| Linux（含 WSL）运行时 | `sudo cp ca.pem /usr/local/share/ca-certificates/multica.crt && sudo update-ca-certificates`；守护进程、`git`、`curl` 都认 |
| Windows 运行时 | 管理员 PowerShell：`Import-Certificate -FilePath ca.pem -CertStoreLocation Cert:\LocalMachine\Root`；Git for Windows 还要 `git config --global http.sslBackend schannel` |
| macOS | `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ca.pem` |
| 浏览器 | 一般跟随系统；Firefox 在 设置 → 隐私与安全 → 证书 → 导入 |

WSL 和 Windows 的证书库是分开的，两边都要装。改完重启守护进程。

## 4. 检查

```sh
curl --cacert data/proxy-certs/ca.pem https://10.0.0.5/health          # Multica 后端
curl --cacert data/proxy-certs/ca.pem -o /dev/null -w '%{http_code}\n' https://10.0.0.5:3443/user/login
curl -m 5 https://<公网 IP>/health                                    # 应连不上
```

浏览器里打开 Multica 后，开发者工具 → 网络 → WS 里 `/ws` 应为 `101`。

## 服务之间的调用不经过代理

同一个 compose 里的服务走内部网络：Multica 调 Gitea 用 `http://gitea:3000`，Gitea 的 Webhook 发到
`http://backend:8080/...`，Actions 作业从 `http://gitea:3000` 拉代码。它们不依赖证书。接入步骤见
[gitea-code-host.zh.md](gitea-code-host.zh.md)。
