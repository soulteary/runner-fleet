# 文档改进计划

对本仓库全部 23 份 Markdown（3,677 行）、6 份界面译文、以及被文档引用到的配置模板与运行时
输出做了一次通读与实测，记录发现的问题、根因、以及分批可落地的修法。

**这份文件是给维护者看的工作计划，不是产品文档**，与 [`ci-recipes-migration.md`](ci-recipes-migration.md)
同类，因此刻意不做译文，也不进 `scripts/ci-recipes.conf` 的 `docs_files`。

**审计基准**：`main` @ `0e812a7`，最新发布 v1.7.1。<!-- version-check-ignore -->

**执行状态**：**第 1 批已完成，第 5 节的五条检查已全部落地**（§1.1、§1.2、§1.4、§1.5、§2.1、
§5.1–§5.5）。第 2 批起未开始。每条检查都做了 mutation 验证：把缺陷放回去，对应的那一条、
且只有那一条会红——记录在 §5 各条末尾。下面的问题描述保留审计当时的原文，
改掉它们会让「为什么要加这条检查」失去凭据；已修的条目在标题上标注。

---

## 0. 先说结论

这个项目的文档水准在同体量的自托管工具里属于上游：`development.md` 逐节交代「为什么这么做」
而不只是「怎么做」，排障条目直接写到根因（pid 文件那条把 actions/runner 三个启动脚本都翻过），
五种译文齐备，还有 CI 守着版本号与译文结构。**问题不在写得不够，而在三件事**：

1. **最短路径没跑通。** 文档里第一段可复制的命令会报错，六份译文同时错。
2. **文档翻了六种语言，程序只会说中文。** 排障步骤要求用户 `grep 自检`；API 返回的提示语
   不论界面语言一律中文。翻译停在了界面外壳，没有覆盖用户真正会读到的字符串。
3. **护栏只守住了能自动检查的那一半。** 版本号检查很严，但它只保证「仓库内部自洽」，
   不保证「与已发布的版本一致」——v1.7.1 已经发布，仓库里没有任何一处指向它。<!-- version-check-ignore -->
   译文结构检查只比标题层级，所以表格少一行、正文少一段都能过。

下面 17 个问题按「修不修得动」和「不修会怎样」排了优先级。每条都给了证据位置，
第 6 节按四批打包成可独立提交的 PR。

---

## 1. P0：照着文档做会失败

### 1.1 快速开始的命令块跑不通，六份译文同错  ✅ 已修

`docs/guide.md:29-38` 以及 zh/fr/de/ja/ko 的同一处（行号完全一致，六份文件都是 191 行）：

```bash
mkdir -p config && cp config.yaml.example config/config.yaml
chown 1001:1001 config runners          # ← runners 此刻还不存在
mkdir -p runners && chown 1001:1001 runners
```

实测：

```
chown: cannot access 'runners': No such file or directory
exit=1
```

两个问题叠在一起：

- **顺序反了**：`chown` 在 `mkdir -p runners` 之前，第一条必定对 `runners` 报错；
  而且第二行把 `runners` 又 chown 了一遍，说明这段是两次修改叠出来的，没人整段跑过。
- **缺 `sudo`**：`config` 由当前用户创建，非 root 用户 `chown 1001:1001` 会
  `Operation not permitted`。`examples/deploy/README.md:26` 写的是正确形态
  （`mkdir -p config runners && sudo chown -R 1001:1001 config runners`），两处不一致。

`docker-compose.yml:8-9` 的首次使用注释里是同一段错误顺序，也要一并改。

**修法**：三处统一成 examples 里那一版：

```bash
mkdir -p config runners && cp config.yaml.example config/config.yaml
sudo chown -R 1001:1001 config runners
```

### 1.2 英/法/德/日/韩文档让用户 grep 一个中文词  ✅ 已修（grep 锚点部分）

`docs/guide.md:99`、`:103`、`:189` 以及五份译文的对应位置：

```bash
docker compose logs runner-manager | grep 自检
```

起动自检是文档里推荐的第一诊断手段（"Anything not working: check the startup self-test first"），
而它的落地形态是一个非中文用户既打不出、也看不懂输出的命令。`examples/runner-images/README.md:113`
同样。

根因不在文档，在代码：`internal/runner/preflight.go`、`internal/handler/handler.go` 等处
的日志与自检标题全是中文字面量，没有走 i18n。

**修法**（短期，纯文档，1 小时内）：把 grep 的锚点从中文词换成语言无关的前缀。
代码侧给自检输出加一个稳定标记，文档统一 `grep '\[preflight'`。
一次性改动，之后译文不用再跟着日志文案走。

**已落地**：标记是 `internal/runner.PreflightLogMarker`（`[preflight ✓]` / `[preflight !]` /
`[preflight ✗]`）。两侧各有一条测试守着：`preflight_test.go` 守每一级都带标记且标记是纯 ASCII，
`internal/docsconsistency/preflight_marker_test.go` 守文档里让人 grep 的词就是这个常量。
**日志正文仍是中文**——那是 §1.3，这里只解决「怎么把这些行捞出来」。

### 1.3 API 提示语不跟随界面语言

界面有 152 个 i18n 键 × 6 种语言（`cmd/runner-manager/i18n/`，且 `i18n_test.go` 交叉校验键集），
但那只覆盖模板里的静态文案。用户操作后真正读到的那句话来自服务端，是中文硬编码：

```go
// internal/handler/handler.go
echo.NewHTTPError(http.StatusBadRequest, "name、target_type、target 必填")
"message": "Runner 已添加，正在后台安装并注册，请稍后刷新页面"
"message": "状态探测失败，但已尝试启动 Runner 并通知 Agent"
```

把界面切到 English，外壳是英文，每一个 toast 是中文。文档承诺了六种语言的使用体验，
产品只兑现了外壳。

**这一条是代码问题，但必须写进文档计划**：要么补 i18n（工作量不小，需要把 handler 的
消息抽成键），要么在 `docs/guide.md` 与 README 里如实写明「界面外壳已翻译，服务端消息
与日志目前仅中文」。**装作没这回事是最差的选项**——它让翻译看起来像没做完，而不是
一个已知的、有边界的限制。

### 1.4 两处指向不存在文件的引用  ✅ 已修

| 位置 | 引用 | 实际 |
|---|---|---|
| `docker-compose.yml:2` | `# 详见 docs/docker.md` | 该文件在本仓库历史中从未存在 |
| `config.yaml.example:3` | `# 详细字段说明见 docs/config.md` | 同上 |

两个文件都是用户第一步就会打开并照着改的。`config.yaml.example` 那条尤其要命：它把人
指向一份不存在的「详细字段说明」，而真正的字段表在 `docs/guide.md` 的第 2 节。

Markdown 之间的相对链接目前是全绿的（写脚本全量查过，0 处断链）——问题恰好出在
**非 Markdown 文件里的文档引用**，没有任何检查覆盖这一块。

**修法**：改成 `docs/guide.md#2-configuration`（及译文对应锚点），并把链接检查扩展到
`*.yml`/`*.example`/`Dockerfile*`（见 §5.3）。

### 1.5 v1.7.1 已发布，仓库里没有一处指向它  ✅ 已修<!-- version-check-ignore -->

- GitHub 最新 release：**v1.7.1**（2026-09-21 11:51，PR #58）<!-- version-check-ignore -->
- `internal/config/config.go:48`：`tag = "v1.7.0"` <!-- version-check-ignore -->
- 全仓库 91 处 `v1.7.0`，0 处 v1.7.1<!-- version-check-ignore -->
- `CHANGELOG.md:8` 的 `## [Unreleased]` 里躺着的三条，正是 v1.7.1 发布的内容<!-- version-check-ignore -->

版本一致性检查照样是绿的——它拿 `config.go` 当基准，比的是「仓库内部自洽」。发布流程在
基准之外，检查天然看不见。`ci-consistency.yml:51` 的注释记着「v1.0.1 / v1.1.0 / v1.1.1 <!-- version-check-ignore -->
三次发布都漏了同步」，这一次是同一类漏，只是漏在了检查管不着的一侧。

**修法**：
1. 补 `## [1.7.1]` 小节 + 底部链接定义，`[Unreleased]` 清空；<!-- version-check-ignore -->
2. `config.go` 的基准 tag 与文档 91 处一起升到 1.7.1；<!-- version-check-ignore -->
3. 加一条 CI（见 §5.1）：打 tag 时校验 `config.go` 基准 == tag，且 CHANGELOG 有对应小节。

---

## 2. P1：内容缺失与语言断层

### 2.1 译文漂移：结构检查只看标题，表格少一行照样过  ✅ 已修

`docs/development.md` 的 HTTP API 表有 **14 行**，五份译文都只有 **13 行**——
少的是同一行：

```
| `/api/runners/:name` | DELETE | Remove a runner: stop it, deregister it from GitHub … |
```

过程是清楚的：`075b612` 给英文 `development.md` 加了 20 行（DELETE 行 + 「Runner removal
and GitHub」一节），译文一份没动；`797959a` 作为补译把 `### 删除 Runner 与 GitHub`
这一节补了回去——**因为它是个标题，结构检查会报错**。表格里那一行不是标题，于是留在了原地。

这正好说明现有护栏的边界：`check-docs-structure` 比的是标题层级序列，
表格行、代码块、链接目标、正文段落一概不比。

**修法**：先补那一行（5 分钟），再把检查扩到表格行数与代码块（见 §5.2）。

### 2.2 examples/ 只有中文，而英文 README 直接往那儿送人

| 文件 | 行数 | 语言 | 谁会点进来 |
|---|---|---|---|
| `examples/deploy/README.md` | 174 | 仅中文 | `README.md` 的 "Deployment examples"、`guide.md` 的 "Ready-made deployment examples" |
| `examples/runner-images/README.md` | 119 | 仅中文 | `README.md` 的 Highlights、`guide.md` 的 "Extending the runner image"、排障两条 |

这 293 行不是边角料，**是全仓库排障密度最高的内容**：容器名冲突、`docker create 失败:
signal: killed`、GitHub 上只出现一个十六进制名字的 Runner、`_work/_actions` 不能共享、
`host-socket` 下 `-v $PWD` 挂空目录……英文 `guide.md` 的排障只有 9 条，
`examples/deploy/README.md` 有 15 条且每条都带根因。

`examples/deploy/README.md:174` 的收尾更把这件事钉死了：它的最后一句是
`其余部署与配置说明见 [使用指南](../../docs/zh/guide.md)`——**写死指向中文版**，
从英文 README 一路点进来的人
被送进中文文档后，再被送回中文文档。

### 2.3 用户会复制走的两个模板文件只有中文

`config.yaml.example`（每个用户第一步 `cp` 走）与 `.env.example`（容器模式的主要配置面）
全文中文注释。英文用户 `cp config.yaml.example config/config.yaml` 之后，得到的是一份
自己读不懂的配置文件——而 §1.4 里那条指向 `docs/config.md` 的「详细字段说明」还是断的。

### 2.4 环境变量没有一处完整清单

代码里从环境变量读的配置项与文档覆盖情况：

| 变量 | guide.md | .env.example | 说明 |
|---|---|---|---|
| `LOG_LEVEL` / `LOG_FORMAT` | ✗ | 仅中文 | v1.7.0 新增的日志配置，正式文档一字未提 | <!-- version-check-ignore -->
| `FLEET_IMAGE_TAG` | ✗ | 仅中文 | 决定默认 Runner 镜像 tag |
| `SERVER_ADDR` / `RUNNERS_BASE_PATH` | ✗ | 仅中文 | |
| `AGENT_PORT` | ✗ | ✗ | 代码里有，两边都没写 |
| `CONTAINER_IMAGE` | ✗ | ✗ | `RUNNER_IMAGE` 的别名，仅代码可见 |
| `RUNNERS_VOLUME_HOST_PATH` | ✗ | ✗ | `VOLUME_HOST_PATH` 的别名，同上 |

`guide.md:157` 只写了「Some fields above can be overridden by environment variables
(e.g. …); see `.env.example`」——把人指向一份中文文件，而那份文件本身还漏了三个变量。

### 2.5 配置字段表不全

`guide.md` 第 2 节的表覆盖了全局字段，但缺：

- `runners.docker_gid`（只在正文里被提过，表里没有）
- `runners.items[]` 的全部字段：`name` / `path` / `target_type` / `target` / `labels`

结果是：想手写 `config.yaml` 的人必须去读 `config.yaml.example`（中文）或
`internal/config/config.go` 的 struct tag。

### 2.6 运维接口写在了贡献者文档里

`/ready`、`/metrics`、`/version` 三个端点只在 `development.md`（"This doc is for
contributors: local build and debug"）里有表格说明。但它们的读者是运维：
`/ready` 是给 K8s `readinessProbe` 的，`/metrics` 需要在 Prometheus 抓取任务里配
`basic_auth`。`guide.md` 里 `/metrics` 出现 **0 次**，`Prometheus` **0 次**。

README 的一句 "Health: `GET /health`; version: `GET /version`" 也停在 v1.7.0 之前的状态。 <!-- version-check-ignore -->

### 2.7 平台限制没写进面向用户的文档

`development.md:139` 说得很清楚：

> Detection needs `/proc`, so it is Linux-only; elsewhere it reports "not running".

`guide.md` 与 README 里 `Linux` 出现 **0 次**。镜像只发 `linux/amd64,linux/arm64`
（四个 workflow 一致），这两件事——**只能跑在 Linux、只有这两个架构**——是选型阶段
就该知道的，现在埋在贡献者文档的第 139 行。

### 2.8 缺三类面向长期使用者的文档

- **升级**：`guide.md` 里 `upgrade` 出现 1 次（在一条排障末尾）。没有「从 1.6 升到 1.7
  要注意什么」的独立位置；CHANGELOG 里有 `### Security` 和破坏性变更，但没人会把
  CHANGELOG 当升级手册读。
- **备份 / 迁移**：README 的卖点是「config is your backup and easy to version」，
  但没有一处写「要备份哪些东西」——实际上还有 `runners/<name>/.credentials*`、
  `.runner`、`.agent_token`、`.github_check_token`，只备份 `config.yaml` 是不够的。
- **反向代理 / HTTPS**：`guide.md` 里 `HTTPS` / `TLS` 各 0 次。安全一节说「默认无鉴权，
  只在内网或 localhost 用」，而 `TRUSTED_ORIGINS` 的存在说明已经有人放在反代后面了。

---

## 3. P2：结构与工程化

### 3.1 缺社区健康文件

`.github/` 下只有 `actions/`、`assets/`、`workflows/`。没有：

- `CONTRIBUTING.md` —— `development.md` 的「Makefile targets」「Tests」两节其实已经写了
  大半（`make check` 是提交前该跑的），只差把它抽成贡献入口
- `SECURITY.md` —— 一个管着 GitHub Runner 注册凭据（`.credentials_rsaparams`）、
  PAT（`.github_check_token`）和 docker.sock 的工具，没有漏洞上报渠道
- Issue / PR 模板 —— 排障需要的信息（版本、`container_mode`、`job_docker_backend`、
  自检输出）每次都得来回问

仓库开着 Issues 和 Wiki，Discussions 关着；Wiki 为空。

### 3.2 docs/ 首页没有 examples 入口

`docs/README.md`（及五份译文）只列 guide / development / CHANGELOG，
`examples` 出现 **0 次**。而根 README 出现 3 次。从 docs 首页进来的人看不到那 293 行部署示例。

`docs/ci-recipes-migration.md`（218 行）同样是孤儿：没有任何 Markdown 链接指向它，
只有 CHANGELOG 正文提过一次文件名。

### 3.3 CHANGELOG 的条目长到没法扫

91 KB / 329 行 ≈ 平均每行 280 字符，最长的两行 2,624 和 2,474 字符。单条示例：

> - The runner config dialog lays its fields out in three columns instead of one.
>   Seventeen fields stacked in a 480px-wide box meant scrolling to read one runner's
>   configuration; the box is now 960px and …（后续还有 100+ 词）

内容本身有价值——这是设计决策的记录。但 Keep a Changelog 的读者是「我要升级，有什么
影响我」，需要的是一句话 + 链接。现在这份文件把「变更记录」和「设计说明」两个用途
压在了一起，两边都不好读。

### 3.4 README 与 guide 大段重复

README（64 行）里的 Quick start、container mode 说明、apt 包基线说明与 `guide.md`
高度重叠，且已经漂移了（§1.1 的命令在两处是两个形态；`/ready`、`/metrics` README 没跟上）。
每次改一处就要记得改另一处，而没有任何检查盯着。

### 3.5 缺「这东西是怎么工作的」

没有一页讲清 Manager / Agent / Runner 容器三者的关系、状态是怎么判定的、
配置漂移检测在比什么。这些信息散落在 `development.md` 的三节（"How running state is
determined"、"Runner directory permissions"、"Runner removal and GitHub"）和
`guide.md` 的容器模式一节里。一张架构图 + 一页说明，能省掉排障时的大量上下文重建。

---

## 4. 根因

四类问题，各有各的成因，修法也不同：

| 根因 | 表现 | 治法 |
|---|---|---|
| **命令块从没被执行过** | §1.1 | CI 里真跑一遍（§5.4） |
| **护栏只守能自动检查的那部分** | §1.5、§2.1 | 把检查扩到表格行、发布 tag、非 md 文件里的引用（§5.1-5.3） |
| **「文档」的边界被划得太窄** | §2.2、§2.3、§2.4 | examples/ 与两个 `.example` 模板纳入文档范围与翻译范围 |
| **翻译停在界面外壳** | §1.2、§1.3 | 要么补 i18n，要么把边界如实写进文档——不能留在「看起来像没做完」 |

值得说清楚的是：**这不是「文档没人管」的项目**。恰恰相反，它有两条 CI 检查、
有专门的一致性配置文件、译文结构出错会红。问题是护栏的形状——它们守住了
「标题层级」和「仓库内部版本自洽」，而实际发生的漂移走的是表格行和发布流程。

---

## 5. 用 CI 固化结论（防回归）

这一节是整个计划里最值钱的部分：没有它，下面的修法会以同样的方式再漂一次。
五条检查，都能落进 `scripts/ci-recipes.conf` + `ci-consistency.yml` 现有的形状里。

### 5.1 发布版本一致性

**已落地**：`.github/actions/check-release-version`，挂在两个 release workflow 的 checkout 之后。
Mutation：以 v1.7.1 对 v1.7.0 基准试跑，基准不符、缺 `## [1.7.1]` 小节、缺链接定义三条同时报出；<!-- version-check-ignore -->
修好后通过。

现有 `check-version-consistency` 拿 `config.go` 当基准，保证仓库内部自洽。
补一条 tag 触发的检查：`release-*.yml` 里断言 `config.go` 的基准 tag == 正在打的 tag，
且 `CHANGELOG.md` 有对应的 `## [X.Y.Z]` 小节。**这条直接挡住 §1.5。**

### 5.2 译文内容（不只是标题）

**已落地**：`internal/docsconsistency/translations_test.go`，按节比表格行、代码块（只数非注释行）、
列表项与解析后的链接目标；语言与文件清单读 `scripts/ci-recipes.conf`，加第七种语言仍只改那一处。
Mutation：删掉 zh 译文里的 DELETE 行 → 红；补回 → 绿。

`check-docs-structure` 扩展，或本地补一条脚本，逐文件比对英文原文与五份译文的：

- 表格行数（按表比）—— 直接挡住 §2.1
- 代码块数量与其中的命令行数
- 链接目标（href 该相同，链接文字该不同）

**mutation 验证**：把 DELETE 那一行从英文表里删掉，检查必须红；加回去必须绿。

### 5.3 链接检查覆盖非 Markdown 文件

**已落地**：`internal/docsconsistency/pathrefs_test.go`。
Mutation：把 `docs/config.md` 那条引用放回 `config.yaml.example` → 红；改回 → 绿。
附带约束：注释里也不能写失效路径，这是有意的。

目前 Markdown 之间零断链，断的全在 `.yml` / `.example` 的注释里（§1.4）。
把 `docs/*.md`、`examples/**` 这类路径引用的检查扩到
`*.yml` `*.yaml` `*.example` `Makefile` `*Dockerfile*` `*.sh`。

### 5.4 快速开始真跑一次

**已落地**：`CI (Consistency)` 的 `Quick start runs` job。就地构建镜像（不拉已发布 tag——
升版本的那个 PR 里新 tag 的镜像还不存在），整段执行抽出来的命令块，再请求 `GET /ready`。
查 `/ready` 而不是 `/health`：后者只要进程活着就恒为 200，挂载目录不可写照样绿。
Mutation：把旧命令块放回去，在 `set -e` 下第一条 `chown` 即退出 1。

新增 job：在干净容器里，把 `guide.md` 第 1 节的命令块抽出来 `set -e` 执行到
`docker compose up -d`，然后 `curl -fsS localhost:8080/health`。
**这条挡住 §1.1 那一类「两次修改叠出来、整段没人跑过」的错误**，
而且它顺带保证了 README / guide / examples 三处命令块不会再各走各的。

### 5.5 examples/ 纳入或明确豁免

**已落地**：豁免写进了 `scripts/ci-recipes.conf` 的注释，并由
`internal/docsconsistency/examples_policy_test.go` 守着——声明为中文单语的必须确实是中文、
且确实没有译文副本。
Mutation：新增一份未登记策略的 `examples/*/README.md` → 红。

`scripts/ci-recipes.conf` 的 `docs_files` 目前是 `development.md guide.md README.md`。
决定 §2.2 之后：要么把 examples 的两份 README 纳入译文检查，
要么在 conf 里写一行注释说明「刻意只保留中文」——像 `ci-recipes-migration.md` 那样
把决定写下来，而不是让它看起来像漏了。

---

## 6. 分批执行

四批，每批一个 PR，互相不阻塞。

### 第 1 批：把路跑通  ✅ 已完成

> 目标：照着文档做不会失败。纯文档改动，无代码风险。

1. §1.1 修正 `guide.md` × 6 + `docker-compose.yml` 的命令块，与 examples 对齐
2. §1.4 `docker-compose.yml:2`、`config.yaml.example:3` 两处断引用改为 `docs/guide.md`
3. §1.5 CHANGELOG 补 `## [1.7.1]`，基准与 91 处版本号升级<!-- version-check-ignore -->
4. §2.1 补回五份译文的 DELETE 表格行
5. §1.2 日志加 `[preflight]` 稳定标记，六份文档的 `grep 自检` 一并换掉

**验收**：在干净机器上逐条复制 `guide.md` 第 1 节的命令，全程零报错，
`curl localhost:8080/health` 返回 200。

### 第 2 批：补内容缺口（2～3 天）

6. §2.4 `guide.md` 新增「环境变量参考」表，收齐 12 个变量（含两个别名与 `AGENT_PORT`），六语言
7. §2.5 配置表补 `docker_gid` 与 `items[]` 五个字段
8. §2.6 `/ready` `/metrics` `/version` 搬进（或复制进）`guide.md` 的运维小节，
   含 Prometheus `basic_auth` 抓取示例；README 的一行同步
9. §2.7 README 与 `guide.md` 开头写明：Linux only、linux/amd64 + linux/arm64
10. §1.3 如实写明服务端消息与日志的语言边界（在补 i18n 之前，这是诚实的过渡态）

### 第 3 批：语言与结构（1 周，含一次拍板；其中第 15 项的五条检查已提前落地）

11. **决策**：examples/ 与两个 `.example` 模板的语言策略（见 §7）
12. 按决策执行；`examples/deploy/README.md:174` 的收尾链接改成语言中立或跟随当前语言
13. §3.2 `docs/README.md` × 6 补 examples 入口；`ci-recipes-migration.md` 挂到
    development.md 的「Releasing」一节下
14. §3.4 README 瘦身：只留「这是什么 / 五个亮点 / 30 秒起步 / 去哪看文档」，
    其余全部指向 `guide.md`，消除双份维护
15. §5.1-5.5 五条 CI 检查落地（这一条可以并到任何一批前面做，越早越省事）

### 第 4 批：长期资产（按需）

16. §3.1 `CONTRIBUTING.md`（从 `development.md` 的 Makefile / Tests 两节抽）、
    `SECURITY.md`、Issue 与 PR 模板
17. §3.5 架构页：Manager / Agent / Runner 容器的关系图 + 状态判定与漂移检测的一页说明
18. §2.8 升级指南、备份清单（`config.yaml` + `runners/<name>/` 下四类文件）、反代与 HTTPS
19. §3.3 CHANGELOG 分层：条目回到一句话 + 链接，设计说明移入 `docs/decisions/`
    （新增条目起用新格式，历史条目不动——重写 329 行的收益不抵风险）

---

## 7. 需要作者拍板的一件事

其余都是执行，这一条是取舍：**examples/ 与两个 `.example` 模板，要不要翻译？**

现状是三套不同的语言策略同时存在：

| 层 | 现状 |
|---|---|
| `README.md` / `docs/` | 英文 + 5 种译文 |
| `examples/` / `config.yaml.example` / `.env.example` | 仅中文 |
| 代码注释 / 日志 / API 消息 | 仅中文 |

三个选项：

- **A. 全翻**（examples 两份 README + 两个模板文件）。收益最大，代价是 293 行 + 两份模板
  进入 5 语言的维护盘，且 §5.2 的内容检查必须先到位，否则漂移只是换个地方发生。
- **B. 只翻模板**（`config.yaml.example` / `.env.example` 改中英双语注释，examples 维持中文）。
  成本最低、收益最集中——这两个文件是**每个用户都会复制走**的，而 examples 是可选深读。
  代价是英文用户的排障体验仍然差一截。
- **C. 维持现状，但把决定写下来**：在 `docs/README.md` 与根 README 注明 examples 为中文，
  并在 `ci-recipes.conf` 里写一行豁免注释。成本接近零，至少消除「看起来像漏了」。

**建议 B，并把 A 作为 examples 的后续目标。** 理由：`config.yaml.example` 是断引用
（§1.4）和语言断层（§2.3）两个问题的交汇点，修它一次解决两件事；而 examples 那 293 行
排障内容的正确去处，长期看其实是 `guide.md` 的排障小节——它现在只有 9 条，
examples 有 15 条且更深。与其翻译一份中文附录，不如把内容收编进已经在翻译的主文档。

---

## 8. 工作量与验收

| 批次 | 工作量 | 验收标准 |
|---|---|---|
| 1 | 半天 | 干净机器照抄 `guide.md` 第 1 节零报错；`/health` 200；五份译文 API 表 14 行 |
| 2 | 2–3 天 | 代码里读取的每个环境变量都能在 `guide.md` 查到；`config.go` 的每个 yaml 字段都在配置表里 |
| 3 | 1 周 | §5 的五条检查全绿；故意制造每类漂移各一次，对应检查必须红 |
| 4 | 按需 | `CONTRIBUTING.md` 能让新贡献者独立跑通 `make check` |

优先级如果只能做一件事：**第 1 批 + §5.4（快速开始在 CI 里真跑一次）**。
前者修好现在就在挡人的问题，后者保证它不会以第三种形态回来。

---

[← 返回文档首页](README.md)
