# 自带 Gitea 代码托管

自部署的 Multica 访问不到现有的代码托管时，可以让它自带一个 Gitea。这是可选的：不开就是纯 Multica，
开了之后两边的关联由服务端自己配好，不需要在页面之间复制令牌和密钥。

## 1. 打开

在 `.env` 里写两行：

```sh
MULTICA_ADDONS=gitea,ci                    # gitea：自带 Gitea；ci：Gitea Actions 执行器（可不要）
GITEA_ROOT_URL=http://<服务器地址>:3300/     # 人和运行时访问 Gitea 用的地址
```

```sh
make selfhost-build        # 或 make selfhost（用官方镜像时）
```

`make` 会按 `MULTICA_ADDONS` 把对应的 compose 文件带上，并在 `.env` 里补上还空着的
`GITEA_DB_PASSWORD` 和 `GITEA_ADMIN_PASSWORD`（随机生成，已有的值不动）。Gitea 管理员是 `vertex-admin`，
密码就是 `.env` 里的 `GITEA_ADMIN_PASSWORD`。

不用 `make` 时，等价的命令是
`docker compose -f docker-compose.selfhost.yml -f docker-compose.gitea.yml --profile ci up -d`，两个密码要自己填。

Gitea 的数据库放在 Multica 的 PostgreSQL 里（单独的库 `gitea` 和同名角色），仓库文件在 `gitea_data` 卷里。
启动顺序：`gitea-db-init` 建库 → `gitea` → `gitea-bootstrap` 建管理员、给 Multica 建服务账号、生成执行器注册令牌 →
`gitea-runner` 注册。一次性服务每次启动都会跑，已有的东西不会重建。

## 2. 自动完成的关联

每个工作区（已有的和以后新建的）会自动得到：

| 内容 | 说明 |
| --- | --- |
| Gitea 组织 | 以工作区的网址标识命名（被占用时加 `ws-` 前缀），仅登录用户可见；管理员（`GITEA_ADMIN_USER`）是组织所有者；带一个 `developers` 团队，成员可以推送组织下所有仓库 |
| 连接 | **设置 → 代码** 里显示为"Gitea · 内置"，由服务端维护，不能断开或重置 |
| Webhook | 配在组织上，PR 和提交状态会同步到任务上 |
| 仓库列表 | 在这个组织下建的仓库（或推上来的现有仓库）自动加入工作区的 **代码仓库**；之后从列表里移除的不会再被加回来 |
| 只读账号 | `<组织>-reader`，在 `readers` 团队里；工作区的守护进程用它克隆和拉取仓库 |
| 智能体账号 | 每个智能体一个 Gitea 账号（按智能体名字生成，重名时加后缀），在 `developers` 团队里；智能体用自己的身份推分支、开 PR |

新建工作区几秒内就绪；服务端启动时和之后每 5 分钟会再核对一遍。

你要做的只有两件事：

1. 用 `vertex-admin` 登录 Gitea，在工作区同名的组织下建仓库（或把现有仓库推上来）。
2. 把同事拉进来：他们自己在 Gitea 注册账号，你在 **组织 → 团队 → developers** 里把他们加为成员。

智能体不用管：建好就有账号。

已存在的同名 Gitea 组织会被直接沿用。谁可以新建工作区用 `DISABLE_WORKSPACE_CREATION` 或
`WORKSPACE_CREATION_ALLOWED_EMAILS` 控制。删除工作区不会删除 Gitea 上的组织和代码。

## 3. 运行时和智能体怎么访问仓库

运行时主机上**不需要配任何 Git 凭据**：

- **克隆和拉取**：守护进程向服务端要仓库列表时，一并拿到工作区只读账号的凭据，只对这个组织的仓库生效。
- **推送和开 PR**：智能体自己的凭据随每个任务下发，`git push` 直接可用；环境里还有 `GITEA_URL`、`GITEA_API_URL`、
  `GITEA_ORG`、`GITEA_USER`、`GITEA_TOKEN`，智能体按内置技能里的说明调 Gitea 接口开 PR。
- **独立评审**：Gitea 不允许批准自己的 PR。每个智能体的账号不同，负责开发和负责评审的智能体天然满足这一点。

智能体归档后，它的账号会被移出 `developers` 团队并禁止登录，令牌随之失效；恢复后放开并加回团队，令牌不变。
账号本身一直保留（不会删除），这样它的提交、PR 和评审记录不会变成"已删除用户"，所以 Gitea 里的智能体账号只增不减。

令牌在 Gitea 里被删掉也不用处理：下发任务前服务端会检查智能体的令牌，失效就当场换新；只读账号的令牌每 5 分钟
核对一次。每个账号只保留最新的一个令牌。智能体自己在环境变量里设了同名变量（`GITEA_TOKEN`、`GIT_CONFIG_COUNT`
等）时，以它自己的为准。

克隆用的凭据需要运行时主机上的 `multica` 是包含这项功能的版本；服务器升级后，在各主机重新执行安装命令。
人用的账号不在此列：同事自己在 Gitea 注册，由你加进 `developers` 团队。

## 设置

都可以在 `.env` 里改：

| 设置 | 默认 | 变量 |
| --- | --- | --- |
| 镜像 | `gitea/gitea:28.4.0`、`gitea/runner:6.0.0` | `GITEA_IMAGE`、`GITEA_RUNNER_IMAGE` |
| 站点名称 | Vertex AI | `GITEA_APP_NAME` |
| 管理员 | `vertex-admin` / `vertex-admin@vertex.local` | `GITEA_ADMIN_USER`、`GITEA_ADMIN_EMAIL` |
| 自助注册 | 开放，带图片验证码 | `GITEA_DISABLE_REGISTRATION`、`GITEA_ENABLE_CAPTCHA` |
| 注册邮箱域名 | 不限 | `GITEA_EMAIL_DOMAIN_ALLOWLIST`（如 `example.com`） |
| 未登录访问 | 什么都看不到 | `GITEA_REQUIRE_SIGNIN_VIEW` |
| HTTP 端口 | 3300，只绑本机（经 nginx 的 3443 访问） | `GITEA_HTTP_PORT`、`GITEA_BIND` |
| SSH 端口 | 2222，只绑本机 | `GITEA_SSH_PORT`、`GITEA_SSH_BIND`（设为内网 IP 才能 SSH 克隆）、`GITEA_SSH_DOMAIN`、`GITEA_DISABLE_SSH` |
| Webhook 可投递的内网地址 | `private,loopback` | `GITEA_WEBHOOK_ALLOWED_HOSTS` |
| 执行器范围 | 整个实例 | `GITEA_RUNNER_ORG`（填组织名，只给这个组织的仓库跑作业） |

端口与 Multica（前端 3000、后端 8080）不冲突。网页安装向导已关闭。服务器有公网 IP 时，绑定地址不要设成
`0.0.0.0`：Docker 映射的端口会绕过宿主机防火墙，见 [self-host-https.zh.md](self-host-https.zh.md)。

**开放注册时务必设 `GITEA_RUNNER_ORG`**，填工作区对应的组织名。执行器通过宿主机的 Docker 套接字起作业容器，
作业里的命令等于宿主机 root 权限；不限范围时，任何注册用户建个仓库写个工作流就能拿到这台机器。改了范围要让
执行器重新注册：`docker compose ... rm -sf gitea-runner && docker volume rm multica_gitea_runner_data`，再启动。

备份时 Multica 和 Gitea 两个库都要备：`pg_dump -d multica` 与 `pg_dump -d gitea`，外加 `gitea_data` 卷。

Multica 用的服务账号是 `multica-service`（管理员），令牌只存在容器之间共享的卷里。别处部署的 Gitea 也可以当作
自带的来用：给后端设 `MULTICA_BUILTIN_GITEA_URL` 等变量，见 `.env.example`。只想要 PR 同步、不要这些自动配置时，
照旧在 **设置 → 代码 → 自托管 Git** 里手工连接。

## 4. 让 PR 和构建结果对上任务

- **关联**：分支名或 PR 标题里带任务编号，例如 `mul-12-fix-login-redirect`；或在 PR 正文写 `Closes MUL-12`。
- **合并后改状态**：任务关联的 PR 全部合并后，任务改为 **设置 → 代码 → PR 合并后，任务状态改为** 里选的状态。
- **构建结果**：Multica 读取的是 Gitea 的**提交状态**，两种来源都可以。
  - Gitea Actions 的流水线会自动写提交状态。`MULTICA_ADDONS` 里带 `ci` 时执行器随之启动、自动注册。
    作业容器加入 compose 的网络，用 `http://gitea:3000` 拉代码，不依赖对外地址和证书。
    作业在执行器通过本机 Docker 套接字启动的容器里跑，所以只让可信的人能推工作流文件（见 `GITEA_RUNNER_ORG`）。
  - 已有的构建或校验脚本跑完后调一次接口，别的机器上的检查也可以这样接入：

    ```sh
    curl -X POST "http://<服务器地址>:3300/api/v1/repos/<组织>/<仓库>/statuses/<提交 SHA>" \
      -H "Authorization: token <有仓库写权限的令牌>" -H "Content-Type: application/json" \
      -d '{"state":"success","context":"verify","description":"checks passed"}'
    ```

    `state` 取 `pending`、`success`、`failure` 或 `error`，`context` 用来区分不同的检查项。
- **合并门禁**：在 Gitea 的 **仓库设置 → 分支保护** 里把必须通过的检查项设为合并前提，并要求至少一个批准。
  Gitea 不允许批准自己的 PR；每个智能体有自己的 Gitea 账号，所以评审必须由另一个智能体来做。这一层由 Gitea
  强制，不依赖智能体自觉。评审批准时可以设置"检查通过后自动合并"，检查一通过就合并。

任务上可以加**唤醒**规则等"关联 PR 的 CI 结束"、"PR 合并"或"出现合并冲突"，让智能体在检查失败或冲突后回来处理。

## 5. 与任务依赖配合

把一个需求拆成子任务时，可以声明谁依赖谁。依赖没完成的子任务不会启动运行；它等的任务全部完成后，
系统直接启动它，不需要负责父任务的智能体或人来推进。互不依赖的分支并行，汇合点等所有输入。

```sh
multica issue create --title "约定接口" --parent MUL-20 --assignee <智能体> --status todo          # 得到 MUL-21
multica issue create --title "实现服务端" --parent MUL-20 --assignee <智能体> --status todo --blocked-by MUL-21
multica issue create --title "实现界面" --parent MUL-20 --assignee <智能体> --status todo --blocked-by MUL-21
multica issue create --title "集成测试" --parent MUL-20 --assignee <智能体> --status todo \
  --blocked-by MUL-22 --blocked-by MUL-23
```

- 等待中的子任务建成 `todo`，不要建成 `backlog`：挡住它的是依赖，`backlog` 会让它在依赖满足后继续停着。
- "完成"指任务到达已完成状态。PR 合并后任务自动完成，下游随之启动；加上分支保护，就是
  "检查通过并合并后才放行下游"。
- **已取消不算完成**。上游被取消后，下游会一直等，需要有人决定：移除依赖（下游立即启动）或一并取消下游。
- **子任务受阻**：子任务被标为阻塞，或被取消而仍有任务在等它时，父任务的负责人（智能体）会被唤醒去处置，
  处置不了再由它把父任务标为阻塞交给人。
- 任务详情标题上方显示它在等谁、谁在等它，可以在那里移除依赖；父任务的子任务列表里，等待中的行有"等待 …"标记。
- 父任务页的子任务列表上方有**依赖图**：从左到右按可执行的先后排列，虚线框是还在等待的任务，红色是被
  已取消的上游卡住的任务；右上角可以展开看整张图，点节点进入对应任务。
- `multica issue dependency list|add|remove` 查看和修改依赖，`add` 会拒绝成环。
- 全部子任务结束时，父任务的负责人照常被唤醒一次。

运行时主机上的 `multica` 需要是包含这些功能的版本。服务器升级后，在各主机重新执行安装命令。

## 注意

- 用 Gitea 的正式地址访问它。通过 `localhost` 调接口创建的 PR，链接里也会是 `localhost`。
- 任务里的 PR 链接打开的是 Gitea 的页面。仓库是私有的而浏览器没有登录 Gitea 时，Gitea 返回 404 而不是跳到登录页；
  先在同一个浏览器里登录 Gitea，或者把仓库设为公开。
- 给 Multica 和 Gitea 配 HTTPS 见 [self-host-https.zh.md](self-host-https.zh.md)。
