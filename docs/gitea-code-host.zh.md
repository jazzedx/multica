# 自建 Gitea 代码托管

自部署的 Multica 访问不到现有的代码托管时，可以在同一台机器上跑一个 Gitea。连上之后，任务、PR 和
构建结果在 Multica 里对得上，PR 合并可以驱动任务完成和后续任务启动。

## 1. 启动 Gitea

在 `.env` 里写上运行时和浏览器访问 Gitea 用的地址（Gitea 会把它写进克隆地址和 Webhook 内容）：

```sh
GITEA_ROOT_URL=http://<服务器地址>:3300/
```

```sh
docker compose -f docker-compose.selfhost.yml -f docker-compose.gitea.yml up -d gitea
```

HTTP 端口 3300，SSH 端口 2222，数据在 `gitea_data` 卷里。自助注册已关闭，第一个管理员用命令行创建：

```sh
docker exec -u git multica-gitea-1 gitea admin user create --admin \
  --username <名字> --email <邮箱> --password <密码> --must-change-password=false
```

## 2. 连接到工作区

1. 在 Gitea 的 **设置 → 应用** 创建一个只读访问令牌（仓库、用户、组织、工单的读取权限）。
2. 在 Multica 打开 **设置 → 代码 → 自托管 Git → 连接**，提供方选 Gitea，填 Gitea 地址和令牌。
3. 连接成功后复制 **Webhook 地址** 和 **Webhook 密钥**。密钥只显示这一次。
4. 在 Gitea 的组织上添加 Webhook（**组织设置 → Webhook → Gitea**），这样组织下所有仓库都生效：
   填上一步的地址和密钥，内容类型 `application/json`，事件勾选 **Pull Request** 和 **Commit Status**。

`docker-compose.gitea.yml` 已允许 Gitea 向内网地址投递 Webhook（Gitea 默认拒绝）。

## 3. 让运行时能访问仓库

智能体推分支、建 PR 用的是**运行时主机自己的 Git 凭据**，Multica 不经手。给每台运行时主机配一个 Gitea
账号的 SSH 密钥或访问令牌，并确认 `git clone` 能通。仓库在 Multica 的 **设置 → 代码 → 代码仓库** 里添加
克隆地址，再到项目的 **资源 → 仓库** 里选用。

## 4. 让 PR 和构建结果对上任务

- **关联**：分支名或 PR 标题里带任务编号，例如 `mul-12-fix-login-redirect`；或在 PR 正文写 `Closes MUL-12`。
- **合并后改状态**：任务关联的 PR 全部合并后，任务改为 **设置 → 代码 → PR 合并后，任务状态改为** 里选的状态。
- **构建结果**：Multica 读取的是 Gitea 的**提交状态**，两种来源都可以。
  - Gitea Actions 的流水线会自动写提交状态。`docker-compose.gitea.yml` 里带一个执行器：在 Gitea 容器里执行
    `gitea actions generate-runner-token`，把结果写进 `.env` 的 `GITEA_RUNNER_REGISTRATION_TOKEN`，再
    `docker compose -f docker-compose.selfhost.yml -f docker-compose.gitea.yml up -d gitea-runner`。
    每个作业跑在执行器通过本机 Docker 套接字启动的容器里，所以只在能推工作流文件的人都可信的环境使用。
    作业容器用 `GITEA_ROOT_URL` 访问 Gitea，这个地址必须从容器里也能访问到。
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

任务上可以加**唤醒**规则等"关联 PR 的 CI 结束"或"PR 合并"，让智能体在检查失败后回来修。
"合并冲突"这个唤醒条件只支持 GitHub。

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
- 暴露到公网之前，先给 Multica 和 Gitea 配 HTTPS，并限制来源地址。
