# DocHub

DocHub 是一个内部源码文档中心。它通过 SSH 克隆项目仓库，读取一个或多个文档目录中的 Markdown，并提供目录树、正文、右侧标题目录、多分支快照、相对图片、跨文档链接、Mermaid 和基础全文搜索。

## 技术架构

- 后端：Go 1.25、Gin、PostgreSQL、`pgx`
- 前端：Vue 3、TypeScript、Vite、Naive UI
- Git：服务器全局 SSH 凭据、bare mirror、临时 worktree、原子文档快照
- 部署：单实例应用容器 + PostgreSQL，前端构建产物由 Go 服务托管

每个项目的每个白名单分支都会生成独立快照。手动同步只更新当前分支；同步失败时保留上一份成功快照。分支名放在查询参数中，因此支持 `feature/docs` 这类包含 `/` 的分支。

## 快速启动

1. 复制配置：

   ```bash
   cp .env.example .env
   ```

2. 编辑 `.env`，至少设置：

   - `DATABASE_URL`：外置 PostgreSQL 连接串
   - `ADMIN_PASSWORD`：初始管理员密码，首次启动创建后不会随环境变量重置
   - `GIT_HOST_ALLOWLIST`：允许访问的 Git 主机，例如 `git.example.com`
   - `SSH_KEY_FILE`：服务端 SSH 私钥路径
   - `SSH_KNOWN_HOSTS_FILE`：可信 known_hosts 路径

3. 启动：

   ```bash
   docker compose up --build
   ```

4. 打开 `http://localhost:8080`，使用 `ADMIN_USERNAME` 和 `ADMIN_PASSWORD` 登录。

Compose 只启动 DocHub 应用，不包含 PostgreSQL。数据库地址、账号和 SSL 参数全部通过 `DATABASE_URL` 提供。首次迁移会创建 `pg_trgm` 扩展，外置数据库账号需要扩展创建权限，或者由 DBA 提前执行：

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
```

Compose 使用宿主机目录保存 Git 镜像和文档快照，默认映射为 `./data:/data`。可以通过 `.env` 中的 `DATA_DIR` 改为绝对路径：

```env
DATA_DIR=/srv/dochub/data
```

服务不会关闭 SSH 主机校验。首次使用前应通过以下命令准备 known_hosts：

```bash
ssh-keyscan -H git.example.com >> ~/.ssh/known_hosts
```

SSH 私钥必须对目标仓库有只读权限。容器启动时会复制到临时目录并交给非 root 用户，不会写入数据库或镜像。

## 本地开发

Compose、终端命令和 Zed 调试统一读取根目录 `.env`。`.env.example` 是可提交模板，实际 `.env` 仅保存在本机且已被 `.gitignore` 忽略。

使用 Zed 调试：

1. 在项目根目录打开 Zed。
2. 打开 Debug 面板，选择“DocHub 后端”。
3. 启动调试，即可在 Go 源码中设置断点。

也可以从终端启动后端：

```bash
make dev-server
```

Zed 和 `make dev-server` 都会读取根目录 `.env`。后端在启动时会直接连接 `DATABASE_URL` 指定的外置 PostgreSQL。使用实际仓库前，请修改 `.env` 中的 `GIT_HOST_ALLOWLIST`；`SSH_KEY_PATH` 留空时使用当前用户的默认 SSH 配置。

前端：

```bash
cd web
pnpm install
pnpm dev
```

Vite 默认监听 `127.0.0.1:5173`，并将 `/api` 代理到 `127.0.0.1:8080`。

## 使用流程

1. 管理员在项目列表点击“接入项目”。
2. 输入项目名称、SSH 仓库地址、一个或多个文档目录以及初始分支列表。文档目录示例为 `docs`、`design`，输入后按回车添加。
3. 系统异步 clone 每个分支并扫描 Markdown。完成后项目卡片显示“已就绪”。
4. 进入项目后可切换分支、阅读文档、搜索当前分支并手动同步当前分支。
5. 在当前文档工具栏点击“下载”，可将该篇原始 Markdown 保存到本地。首版仅支持单篇下载，不提供批量或目录打包下载。
6. 项目创建者或管理员可在“项目管理”编辑仓库配置、添加或移除分支、删除项目。

项目分享路径使用 SSH/HTTP 仓库地址的最后一段，例如 `git@git.example.com:team/collie-server.git` 对应 `/projects/collie-server`。路径会统一转为小写并移除 `.git` 后缀；不同仓库最后一段重名时，创建项目会提示路径冲突。

左侧目录会分别展示每个已配置的文档根目录，只显示 Markdown 文件和包含 Markdown 的目录。多个文档目录会合并到同一分支快照中，文件路径保留仓库内的完整目录前缀；目录不能重复，也不能互相包含。右侧目录提取二级至四级标题，并随滚动高亮当前位置。

## 配置项

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `APP_ADDR` | `:8080` | HTTP 监听地址 |
| `DATABASE_URL` | 无，必填 | 外置 PostgreSQL 连接串 |
| `STORAGE_DIR` | `./data` | 仓库镜像与快照存储目录 |
| `WEB_DIR` | `./web/dist` | 前端构建产物目录 |
| `GIT_HOST_ALLOWLIST` | 无，必填 | 允许访问的 Git 主机，多项使用英文逗号分隔；支持 `host:port` 写法 |
| `SSH_KEY_PATH` | 空 | 宿主机或本地运行时使用的 SSH 私钥 |
| `SSH_KNOWN_HOSTS_PATH` | 空 | known_hosts 文件 |
| `ADMIN_USERNAME` | `admin` | 初始管理员用户名 |
| `ADMIN_PASSWORD` | 无，必填 | 初始管理员密码 |
| `SESSION_TTL` | `168h` | 登录会话有效期 |
| `COOKIE_SECURE` | `false` | 启用 HTTPS 时应设为 `true` |

生产环境应在应用前配置 HTTPS 反向代理，并设置 `COOKIE_SECURE=true`。登录 Cookie 为 `HttpOnly`、`SameSite=Lax`；所有写请求同时校验 CSRF Token。

## 验证

后端测试：

```bash
make test
```

前端按照项目约定仅执行 TypeScript/SFC 语法检查：

```bash
make typecheck
```

建议的端到端验证仓库至少包含：

- `docs/README.md` 和多级子目录
- `design/README.md` 等第二个文档目录
- 中文文件名及重复标题
- 指南之间的相对 Markdown 链接
- `docs/images/` 下的相对图片
- 表格、任务列表、代码块和 Mermaid
- `main` 与 `feature/docs` 两个分支的文档差异

## 安全边界

- 仓库地址只允许 SSH 形式，并受 `GIT_HOST_ALLOWLIST` 限制。
- Git 子进程不经过 shell，禁用终端密码提示并强制 known_hosts 校验。
- 文档路径、资源路径和符号链接均校验在文档根目录内。
- Markdown 原始 HTML 关闭，渲染结果再次经过 DOMPurify 清理。
- 所有登录成员可读；项目编辑、分支管理和同步仅项目创建者或管理员可操作。
