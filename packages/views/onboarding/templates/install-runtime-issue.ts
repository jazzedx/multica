import type { DaemonCommands } from "@multica/core/runtimes";

/**
 * Skip path: "Connect a runtime to start with Mika".
 *
 * Written to a new issue (assigned to the user themselves) by OnboardingFlow
 * when the user takes the Skip exit on Step 3, which then opens it. Content
 * is the install-runtime tutorial for this deployment: Claude Code and Codex
 * as the agent CLIs, and the deployment's own CLI install and connect
 * commands, so the CLI a machine installs matches the server.
 */

/**
 * Localized so users see the title in their current supported locale on the
 * board. The Runtimes page owns the follow-up Mika bootstrap once a runtime
 * appears, so this guide does not ask the member to copy an agent prompt.
 *
 * Note: server's deprecation shim (`onboarding_shim.go:noRuntimeIssueTitle`)
 * still uses the bare English string for its title-based dedupe — that
 * codepath only runs for pre-v3 desktop builds and never overlaps with
 * the v3 frontend population, so the two title-spaces drifting is fine.
 */
export const INSTALL_RUNTIME_ISSUE_TITLE = {
  en: "Connect a runtime to start with Mika",
  zh: "连接运行时，和 Mika 开始",
  ko: "runtime을 연결하고 Mika와 시작하기",
  ja: "runtime を接続して Mika と始める",
} as const;

type GuideLang = keyof typeof INSTALL_RUNTIME_ISSUE_TITLE;

interface GuideCopy {
  intro: string;
  toolsHeading: string;
  toolsLede: string;
  unixLabel: string;
  windowsLabel: string;
  claudeWindows: string;
  signIn: string;
  claudeSignIn: string;
  codexInstall: string;
  connectHeading: string;
  connectLede: string;
  connectSetup: string;
  connectFallback: string;
  onlineHeading: string;
  onlineBody: string;
  notesHeading: string;
  notes: string[];
  beforeRuntime: string;
}

const COPY: Record<"en" | "zh", GuideCopy> = {
  en: {
    intro:
      "Welcome to Multica.\n\nAgents work on a **runtime**: a computer with an AI coding tool and the Multica daemon installed. It can be your own computer, a server, a VM, or WSL. Connect your first one in three steps.",
    toolsHeading: "## 1. Install an AI coding tool",
    toolsLede:
      "Multica works with many AI coding tools, such as Claude Code, Codex, Cursor, GitHub Copilot, OpenCode, and Kimi. Install any one of them; with several installed, Multica picks up each. The steps below use Claude Code and Codex as examples. Sign in once in a terminal after installing.",
    unixLabel: "macOS / Linux / WSL:",
    windowsLabel: "Windows PowerShell:",
    claudeWindows: "Windows PowerShell (install Git for Windows first):",
    signIn: "Sign in:",
    claudeSignIn: "run `claude` and follow the prompts.",
    codexInstall: "Install (needs Node.js):",
    connectHeading: "## 2. Install the Multica CLI and connect",
    connectLede:
      "Run these on the same computer. They already point at this server, so the CLI you install matches it.",
    connectSetup:
      "Then run the command below. It opens a browser for you to approve the sign-in and starts the daemon in the background:",
    connectFallback:
      "Open **Runtimes** in the sidebar and run the commands shown there to install the Multica CLI and connect.",
    onlineHeading: "## 3. Check it is online, then start with Mika",
    onlineBody:
      "Open **Runtimes** in the sidebar. the AI coding tool on this computer should show as online, and the page offers **Start with Mika**: it creates Mika and opens the first chat. Mika turns one real goal into issues and hands them to the right agent.\n\nAn AI coding tool installed later is picked up within a few minutes. To apply it right away, run `multica daemon restart`.",
    notesHeading: "## More",
    notes: [
      "**Code repositories**: agents clone and push with the Git credentials of the runtime computer, so sign it in to your code host first. To link pull requests to issues, connect the code host in **Settings → Code**.",
      "**Upstream docs**: the original Multica guide to installing a runtime, including every supported AI coding tool, is at https://multica.ai/docs/install-agent-runtime (source: https://github.com/multica-ai/multica).",
    ],
    beforeRuntime:
      "Until a runtime is connected, you can already use Multica for project management: create a project, create issues and move them across statuses, add labels and comments, and follow your assignments in Inbox.",
  },
  zh: {
    intro:
      "欢迎来到 Multica。\n\n智能体在**运行时**上干活。运行时就是一台装了 AI 编程工具和 Multica 守护进程的电脑：可以是你自己的电脑，也可以是服务器、虚拟机或 WSL。按下面三步连上第一台。",
    toolsHeading: "## 1. 安装 AI 编程工具",
    toolsLede:
      "Multica 支持多种 AI 编程工具，例如 Claude Code、Codex、Cursor、GitHub Copilot、OpenCode、Kimi。装任意一个就行，装了多个也会分别识别。下面以 Claude Code 和 Codex 为例。装好后先在终端里登录一次。",
    unixLabel: "macOS / Linux / WSL：",
    windowsLabel: "Windows PowerShell：",
    claudeWindows: "Windows PowerShell（需要先装 Git for Windows）：",
    signIn: "登录：",
    claudeSignIn: "运行 `claude`，按提示登录。",
    codexInstall: "安装（需要 Node.js）：",
    connectHeading: "## 2. 安装 Multica 命令行并连上",
    connectLede:
      "在同一台电脑上运行。命令里已经填好了这台服务器的地址，装上的命令行版本和服务器一致。",
    connectSetup: "然后运行下面这条命令。它会打开浏览器让你确认登录，并在后台启动守护进程：",
    connectFallback:
      "打开左侧的**运行时**页面，按页面上的命令安装 Multica 命令行并连接。",
    onlineHeading: "## 3. 确认在线，和 Mika 开始",
    onlineBody:
      "打开左侧的**运行时**页面，应该能看到这台电脑上的 AI 编程工具显示在线。页面上会出现**和 Mika 开始**，点击后会创建 Mika 并进入首次对话：Mika 会把一个真实目标拆成任务，交给合适的智能体执行。\n\n之后再装的 AI 编程工具，守护进程几分钟内会自动识别；想立刻生效，运行 `multica daemon restart`。",
    notesHeading: "## 更多",
    notes: [
      "**代码仓库**：智能体用运行时电脑上的 Git 凭据克隆和推送，先在这台电脑上登录你的代码托管。想让 PR 关联到任务，在**设置 → 代码**里连接代码托管。",
      "**上游原版文档**：Multica 官方的运行时安装说明，包括支持的全部 AI 编程工具，见 https://multica.ai/docs/install-agent-runtime （源码仓库 https://github.com/multica-ai/multica）。",
    ],
    beforeRuntime:
      "运行时连上之前，也可以先把 Multica 当项目管理工具用：建项目、建任务并在各状态间流转、加标签和评论、在收件箱里跟进分配给你的事。",
  },
};

/** A labelled shell command, as a fenced block so it renders copyable. */
const command = (label: string, shell: "bash" | "powershell", cmd: string) =>
  `${label}\n\n\`\`\`${shell}\n${cmd}\n\`\`\``;

/**
 * Build the guide body. `commands` are this deployment's CLI install and
 * connect commands; when they are not known yet the guide points to the
 * Runtimes page, which shows the same commands once they are.
 *
 * Only English and Simplified Chinese are authored; other languages read the
 * English guide.
 */
export function buildInstallRuntimeIssueBody(
  lang: GuideLang,
  commands: DaemonCommands,
): string {
  const c = COPY[lang === "zh" ? "zh" : "en"];
  const connect =
    commands.status === "ready"
      ? [
          c.connectLede,
          "",
          command(c.unixLabel, "bash", commands.installCmds.macosLinux),
          "",
          command(c.windowsLabel, "powershell", commands.installCmds.windows),
          "",
          command(c.connectSetup, "bash", commands.setupCmd),
        ]
      : [c.connectFallback];

  return [
    c.intro,
    "",
    c.toolsHeading,
    "",
    c.toolsLede,
    "",
    "### Claude Code",
    "",
    command(c.unixLabel, "bash", "curl -fsSL https://claude.ai/install.sh | bash"),
    "",
    command(c.claudeWindows, "powershell", "irm https://claude.ai/install.ps1 | iex"),
    "",
    `${c.signIn}${c.claudeSignIn}`,
    "",
    "### Codex",
    "",
    command(c.codexInstall, "bash", "npm install -g @openai/codex"),
    "",
    `${c.signIn}\`codex login\``,
    "",
    c.connectHeading,
    "",
    ...connect,
    "",
    c.onlineHeading,
    "",
    c.onlineBody,
    "",
    c.notesHeading,
    "",
    ...c.notes.map((note) => `- ${note}`),
    "",
    c.beforeRuntime,
  ].join("\n");
}
