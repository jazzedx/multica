# 自建 Gitea 代码托管

自部署的 Multica 访问不到现有的代码托管时，可以在同一台机器上跑一个 Gitea。连上之后，任务、PR 和
构建结果在 Multica 里对得上，PR 合并可以驱动任务完成和后续任务启动。

## 1. 启动 Gitea

Gitea 的数据库放在 Multica 的 PostgreSQL 里（单独的库 `gitea` 和同名角色），仓库文件在 `gitea_data` 卷里。
在 `.env` 里写上：

```sh
# 人和运行时访问 Gitea 用的地址，Gitea 会把它写进克隆地址、页面链接和 Webhook 内容。
GITEA_ROOT_URL=http://<服务器地址>:3300/
GITEA_DB_PASSWORD=<openssl rand -hex 24>
GITEA_ADMIN_PASSWORD=<管理员密码>
```

```sh
docker compose -f docker-compose.selfhost.yml -f docker-compose.gitea.yml --profile ci up -d
```

启动顺序：`gitea-db-init` 建库 → `gitea` → `gitea-bootstrap` 建管理员 `vertex-admin` 并生成执行器注册令牌 →
`gitea-runner` 注册。几个一次性服务每次 `up` 都会跑，已有的东西不会重建；去掉 `--profile ci` 就不启动执行器。

默认设置（都可以在 `.env` 里改）：

| 设置 | 默认 | 变量 |
| --- | --- | --- |
| 镜像 | `gitea/gitea:28.4.0`、`gitea/runner:6.0.0` | `GITEA_IMAGE`、`GITEA_RUNNER_IMAGE` |
| 站点名称 | Vertex AI | `GITEA_APP_NAME` |
| 管理员 | `vertex-admin` / `vertex-admin@vertex.local` | `GITEA_ADMIN_USER`、`GITEA_ADMIN_EMAIL` |
| 自助注册 | 开放，带图片验证码 | `GITEA_DISABLE_REGISTRATION`、`GITEA_ENABLE_CAPTCHA` |
| 注册邮箱域名 | 不限 | `GITEA_EMAIL_DOMAIN_ALLOWLIST`（如 `example.com`） |
| 未登录访问 | 什么都看不到 | `GITEA_REQUIRE_SIGNIN_VIEW` |
| HTTP 端口 | 3300，只绑本机 | `GITEA_HTTP_PORT`、`GITEA_BIND`（设为内网 IP 才能从别的机器访问） |
| SSH 端口 | 2222，只绑本机 | `GITEA_SSH_PORT`、`GITEA_SSH_BIND`（设为内网 IP 才能 SSH 克隆）、`GITEA_SSH_DOMAIN`、`GITEA_DISABLE_SSH` |
| Webhook 可投递的内网地址 | `private,loopback` | `GITEA_WEBHOOK_ALLOWED_HOSTS` |
| 执行器范围 | 整个实例 | `GITEA_RUNNER_ORG`（填组织名，只给这个组织的仓库跑作业） |

端口与 Multica（前端 3000、后端 8080）不冲突。网页安装向导已关闭。服务器有公网 IP 时，绑定地址不要设成
`0.0.0.0`：Docker 映射的端口会绕过宿主机防火墙。

**开放注册时务必设 `GITEA_RUNNER_ORG`。** 执行器通过宿主机的 Docker 套接字起作业容器，作业里的命令等于宿主机 root
权限；不限范围时，任何注册用户建个仓库写个工作流就能拿到这台机器。设了组织后，`gitea-bootstrap` 会在组织不存在时
创建它，执行器只为这个组织的仓库服务。改了范围要让执行器重新注册：
`docker compose ... rm -sf gitea-runner && docker volume rm multica_gitea_runner_data`，再 `up`。

备份时 Multica 和 Gitea 两个库都要备：`pg_dump -d multica` 与 `pg_dump -d gitea`，外加 `gitea_data` 卷。

## 2. 连接到工作区

Multica 和 Gitea 在同一个 compose 里，两者之间的调用都走内部网络，不经过代理和证书：

1. 在 Gitea 的 **设置 → 应用** 创建一个只读访问令牌（仓库、用户、组织、工单的读取权限）。
2. 在 Multica 打开 **设置 → 代码 → 自托管 Git → 连接**，提供方选 Gitea，Gitea 地址填 `http://gitea:3000`，再填令牌。
3. 连接成功后复制 **Webhook 地址** 和 **Webhook 密钥**。密钥只显示这一次。
4. 用管理员在 Gitea **站点管理**里添加**系统 Webhook**（类型选 Gitea），对实例里
   所有仓库生效，以后新建的组织和仓库不用再配。只想覆盖一个组织时，改在 **组织设置 → Webhook** 里加。
   - 地址用上一步地址的路径，主机换成后端：`http://backend:8080/api/webhooks/vcs/<连接 ID>`
   - 密钥填上一步的密钥，内容类型 `application/json`
   - 事件勾选 **Pull Request** 和 **Commit Status**

`backend` 是内网地址，`docker-compose.gitea.yml` 默认允许 Gitea 向内网地址投递 Webhook（Gitea 默认拒绝）。

## 3. 让运行时能访问仓库

智能体推分支、建 PR 用的是**运行时主机自己的 Git 凭据**，Multica 不经手。给每台运行时主机配一个 Gitea
账号的 SSH 密钥或访问令牌，并确认 `git clone` 能通。仓库在 Multica 的 **设置 → 代码 → 代码仓库** 里添加
克隆地址，再到项目的 **资源 → 仓库** 里选用。

## 4. 让 PR 和构建结果对上任务

- **关联**：分支名或 PR 标题里带任务编号，例如 `mul-12-fix-login-redirect`；或在 PR 正文写 `Closes MUL-12`。
- **合并后改状态**：任务关联的 PR 全部合并后，任务改为 **设置 → 代码 → PR 合并后，任务状态改为** 里选的状态。
- **构建结果**：Multica 读取的是 Gitea 的**提交状态**，两种来源都可以。
  - Gitea Actions 的流水线会自动写提交状态。执行器随 `--profile ci` 启动、自动注册（见第 1 节）。
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
  Gitea 不允许批准自己的 PR，所以负责开发和负责评审的智能体要用**不同的 Gitea 账号**。这一层由 Gitea
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

- 等待中的子任务建成 `todo`，不要建成 `backlog`：挡住它的是依赖，`backlog` 会让它在依赖结束后继续停着。
- 依赖**结束**即放行：上游到达已完成或已取消状态。PR 合并后任务自动完成，下游随之启动；加上分支保护，就是
  "检查通过并合并后才放行下游"。
- **上游被取消也会放行下游**，被唤醒的智能体会得知哪些前置被取消，需要先确认还能不能继续。
- **子任务受阻**：子任务被标为阻塞，或被取消而有未开始的任务在等它，或完成后又被重新打开而已有任务基于它开工时，
  父任务的负责人（智能体）会被唤醒去重新安排，处置不了再由它把父任务标为阻塞交给人。
- 任务详情的**任务关系**里列出它被谁阻塞、它阻塞了谁，可以在那里增删；列表、看板和表格上有阻塞标记。
- 父任务页的子任务列表上方有**依赖图**：从左到右按可执行的先后排列，虚线框是还在等待的任务，红色连线来自
  已取消的上游，黄色是上游没完成就已开工的任务；右上角可以展开看整张图，点节点进入对应任务。
- `multica issue update <任务> --blocked-by <任务>` / `--remove-blocked-by <任务>` 修改依赖，
  `multica issue list --blocked` 列出在等待的任务；成环会被拒绝。
- 全部子任务结束时，父任务的负责人照常被唤醒一次。

运行时主机上的 `multica` 需要是包含这些功能的版本。服务器升级后，在各主机重新执行安装命令。

## 注意

- 用 Gitea 的正式地址访问它。通过 `localhost` 调接口创建的 PR，链接里也会是 `localhost`。
- 任务里的 PR 链接打开的是 Gitea 的页面。仓库是私有的而浏览器没有登录 Gitea 时，Gitea 返回 404 而不是跳到登录页；
  先在同一个浏览器里登录 Gitea，或者把仓库设为公开。
- 暴露到公网之前，先给 Multica 和 Gitea 配 HTTPS，并限制来源地址。
