# 私有部署的开关

二开只在企业内部以网页端运行。上游面向公开产品的入口不是删掉，而是关掉：代码和上游一致，
关与不关由下面两处决定。升级上游版本时，这些入口背后的代码不会产生冲突。

## 网页端：`apps/web/platform/deployment.ts`

这个文件在页面渲染前执行一次，把 `@multica/core/deployment` 里的开关设成私有部署的取值。
开关默认全开，也就是上游的样子；共享组件的测试不经过这个文件，所以上游的测试不用为二开改写。

| 开关 | 关掉后不再出现 |
| --- | --- |
| `marketing` | 更新日志、Discord、GitHub 标星、标星提示、新版本公告、注册来源问卷 |
| `desktopApp` | 下载桌面端的提示、"在桌面端打开"、登录页的桌面端下载链接 |
| `hostedRuntimes` | Qoder 等厂商托管的运行时，以及"即将推出"的云端服务 |
| `channels` | 列表以外的消息渠道；现在只有 `lark`（飞书） |

要重新开放某一项，改这个文件里对应的值即可。新增入口时，在渲染入口的地方读
`deploymentOffers()`，不要删上游的组件。

## 服务端：`server/cmd/server/deployment.go`

由 `main` 在启动时应用，路由表、处理函数和它们的测试保持上游原样。

- **环境变量默认值**：`DO_NOT_TRACK=1`、`ANALYTICS_DISABLED=true`。上游的自托管遥测默认开启并发往
  Multica 的收集端，这里默认关闭；运维显式把变量设成 `0` 或 `false` 才会打开。
- **不提供的路由**：路由照常注册，请求到达时返回 404。按路由模式匹配而不是按请求路径匹配，
  所以名字恰好叫 `slack` 的工作区不受影响，上游在同一类下新增的路由也会自动关闭。
  - 路径段为 `slack`、`dingtalk`、`wecom`、`weixin`、`telegram`、`qoder` 的路由；
  - `/api/cloud-billing`、`/api/cloud-subscriptions`、`/api/cloud-runtime`、`/api/webhooks/stripe`、
    `/api/contact-sales` 及其下的路由。

## 上游自带开关、二开不用改的

- 计费界面由功能开关 `billing_workspace_subscriptions` 控制，默认关闭。
- 云端运行时入口由 `NEXT_PUBLIC_ENABLE_CLOUD_RUNTIME` 控制，默认关闭。
- 前端埋点在服务端不返回 PostHog 密钥时不初始化；Google Analytics 只有设置了
  `GOOGLE_ANALYTICS_ID` 才加载。

## 仍然直接删除的

- 营销页（`apps/web/app/(landing)`、`apps/web/features/landing`）：带上游品牌和对外内容。
- 商业许可证目录 `server/ee`、`packages/ee`。
