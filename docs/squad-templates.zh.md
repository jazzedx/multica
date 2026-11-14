# 小队模板

小队模板是现成的小队：一个小队，连同它的智能体、每个智能体的指令和技能。工作区的所有者和管理员在
**小队**页面点**从模板创建**，选一个模板安装。

模板是内容，不属于平台代码。平台只提供读取、安装和卸载的机制；有哪些模板、模板里写什么，由部署平台的
组织自己维护，放在一个单独的目录里（通常是一个 git 仓库）。**新部署默认没有模板**，配置了模板目录之后
才会出现。

## 给服务端配置模板目录

服务端从环境变量 `MULTICA_SQUAD_TEMPLATES_DIR` 指向的目录读取模板。没有设置时，模板列表为空。

用自托管的 compose 部署时，不用直接设这个变量，在 `.env` 里启用附加件：

```bash
MULTICA_ADDONS=squad-templates
MULTICA_SQUAD_TEMPLATES_PATH=/srv/multica-squad-templates
```

- `MULTICA_SQUAD_TEMPLATES_PATH` 是模板目录在宿主机上的绝对路径。
- `MULTICA_ADDONS` 已经有别的值时，用逗号隔开追加，例如 `proxy,squad-templates`。
- 改完后执行 `make selfhost` 或 `make selfhost-build`。附加件把这个目录以只读方式挂进后端容器。

不经过 `make` 时，把 `docker-compose.squad-templates.yml` 加进 compose 的文件列表即可。

## 更新模板

直接更新宿主机上的目录（例如 `git pull`）。服务端每次被请求时都重新读取目录，所以**不需要重启**，
下一次打开模板列表就是新内容。

已经安装过模板的工作区不受影响：安装是把内容复制进工作区，之后工作区里的智能体和技能与模板目录无关。
想用上新内容，在工作区里再安装一次同一个模板，见[再次安装](#再次安装)。

## 目录结构

```text
manifest.json                  模板清单
<root>/                        清单里 root 指向的模板树
  agents/<代号>.md             智能体的指令
  squad/<模板>/squad.md        小队的指令（路径由清单指定）
  skills/<任意分组>/<技能>/    技能目录
    SKILL.md
    scripts/、references/ …    技能自带的文件
```

`<root>` 以外的文件和目录（`README.md`、`.git` 等）会被忽略。

### manifest.json

```json
{
  "root": "zh_CN",
  "templates": [
    {
      "key": "bug-fix",
      "recommended": false,
      "title": { "en": "Bug fix", "zh": "缺陷修复" },
      "summary": { "en": "…", "zh": "…" },
      "squad": {
        "name": "缺陷修复小队",
        "description": "…",
        "instructions_file": "squad/bug-fix/squad.md",
        "leader": "leader"
      },
      "agents": ["leader", "backend-developer", "tester"]
    }
  ],
  "agents": {
    "leader": { "name": "Leader", "role": "Leader", "skills": ["multica-verification"] },
    "backend-developer": { "name": "BackendDev", "role": "BackendDev", "skills": ["multica-backend-impl"] },
    "tester": { "name": "Tester", "role": "Tester", "skills": [] }
  }
}
```

| 字段 | 含义 |
| --- | --- |
| `root` | 模板树所在的子目录，必填 |
| `templates[].key` | 模板的标识，不能为空，不能重复。工作区用它记住装过哪个模板，定了之后不要改 |
| `templates[].recommended` | 是否在选择界面里标为推荐 |
| `templates[].title`、`summary` | 选择界面里显示的标题和简介，中英文都要写 |
| `templates[].squad.name` | 安装后小队的名称，必填 |
| `templates[].squad.description` | 小队的描述 |
| `templates[].squad.instructions_file` | 小队指令文件，相对 `root` 的路径 |
| `templates[].squad.leader` | 队长的代号，必须是 `agents` 里列出的一个 |
| `templates[].agents` | 这个模板用到的智能体代号，按显示顺序排列 |
| `agents.<代号>.name` | 智能体的名称，必填。同一个模板里不能重名 |
| `agents.<代号>.role` | 智能体在小队里的角色说明 |
| `agents.<代号>.skills` | 智能体携带的技能名，对应 `SKILL.md` 里的 `name` |

智能体在 `agents` 里只定义一次，可以被多个模板引用。

### 指令文件

智能体的指令放在 `<root>/agents/<代号>.md`，小队的指令放在清单指定的文件里。

文件里如果有一个 ```` ```text ```` 代码块，**只有这个代码块的内容**会成为指令，块外面的文字是写给维护者看的
说明。没有这样的代码块时，整个文件就是指令。指令不能为空。

小队指令里用 `@名称` 称呼成员，例如 `@BackendDev`。名称要和清单里的 `name` 一致，否则队长无法把工作交给
这个成员。

### 技能

`<root>/skills/` 下任何包含 `SKILL.md` 的目录都是一个技能，中间可以有任意层分组目录。

- 技能名取自 `SKILL.md` 开头的 `name`，必填，在整个目录里不能重复。
- 技能目录里的其他文件会随技能一起安装，但以下文件除外：
  - 以 `.` 开头的文件和目录（包括 `.env.example` 这类凭据示例）；
  - `__pycache__`、`__MACOSX`；
  - `LICENSE`、`LICENSE.md`、`LICENSE.txt`。
- 技能文件必须是 UTF-8 文本。二进制文件会让整个目录加载失败。

## 检查模板目录

改完模板后，在提交之前先检查：

```bash
multica squad template check /path/to/templates
```

它按服务端的方式读取目录，列出每个模板的智能体数和技能数。以下情况会报错并以非零状态退出：

- 目录读不出来：清单缺失或格式不对，引用了不存在的智能体或技能，缺少指令文件，技能重名等。
- 模板能读出来但安装后会出问题：
  - 标题或简介缺少中文或英文；
  - 小队指令里没有用 `@名称` 提到某个成员；
  - 技能没有描述或没有内容。

目录有问题时，服务端会拒绝整个目录，模板列表加载失败，而不是只提供其中一部分。所以检查不过的
内容不要更新到服务器上。

## 安装做了什么

安装一个模板会在工作区里建立小队、小队里的智能体，以及这些智能体用到的技能。

安装前界面会对照当前工作区预览，逐项说明：

- **新建**：工作区里还没有的小队、智能体、技能；
- **复用**：已经存在、可以直接用的；
- **更新**：模板里的内容比工作区里的新；
- **保留**：工作区里的内容在安装之后被人改过，默认不覆盖。

只有需要新建智能体时才会让你选运行时，已有的智能体保持原来的运行时。

### 再次安装

模板目录更新后，在工作区里再安装一次同一个模板即可升级。每个由模板安装的技能都记着安装时的内容指纹：

- 工作区里的技能没被改过，而模板里的变了：更新为模板的新内容；
- 工作区里的技能被改过：保留工作区的版本，除非你选择覆盖；
- 两边一样：不动。

## 卸载

**从模板创建**对话框里的**卸载...**可以把一个模板撤掉：

- 它的小队和智能体被归档，可以在智能体页恢复；
- 它的技能被删除，不能恢复；
- 安装之后被改动过的内容，以及还被别的小队或智能体用着的内容会保留，除非你选择一并移除。
