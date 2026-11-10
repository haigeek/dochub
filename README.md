# DocHub

DocHub 是团队内部源码文档中心。它通过 SSH 克隆项目仓库，读取一个或多个文档目录中的 Markdown 和 HTML 文档，并提供目录树、正文、右侧标题目录、多分支快照、相对图片、跨文档链接、Mermaid、HTML 沙箱预览和基础全文搜索。

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

3. 在 `.env` 中设置 `DOCHUB_IMAGE`，填写已经构建并推送的镜像地址，例如：

   ```env
   DOCHUB_IMAGE=registry.example.com/dochub/dochub:1.0.0
   ```

4. 拉取镜像并启动：

   ```bash
   docker compose pull
   docker compose up -d
   ```

5. 打开 `http://localhost:8080`，使用 `ADMIN_USERNAME` 和 `ADMIN_PASSWORD` 登录。

Compose 只启动 DocHub 应用，不包含 PostgreSQL，也不会在启动时构建代码。数据库地址、账号和 SSL 参数全部通过 `DATABASE_URL` 提供。首次迁移会创建 `pg_trgm` 扩展，外置数据库账号需要扩展创建权限，或者由 DBA 提前执行：

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

SSH 私钥必须对目标仓库有只读权限。当前环境的通用同步用户为 `Jenkinsadmin`，仓库创建人或 GitLab Group 管理员需要将该用户添加为仓库成员并授予只读权限。容器启动时会把私钥复制到临时目录并交给非 root 用户，不会写入数据库或镜像。

## Docker 镜像构建与发布

生产部署使用预构建镜像。首次使用 Buildx 时，创建并选择一个独立的 `docker-container` builder：

```bash
docker buildx create \
  --name dochub-builder \
  --driver docker-container \
  --use
docker buildx inspect --bootstrap
```

如果本机已经有可用的 Buildx builder，可以跳过创建步骤。

构建仅用于本机运行的单架构镜像：

```bash
docker buildx build \
  --tag dochub:latest \
  --load \
  .
```

此时将 `.env` 中的 `DOCHUB_IMAGE` 设置为 `dochub:latest`，直接启动 Compose：

```bash
docker compose up -d
```

构建并推送同时支持 `linux/amd64` 和 `linux/arm64` 的镜像。先登录镜像仓库：

```bash
docker login registry.example.com
```

然后使用带版本号和 `latest` 标签的方式构建、推送：

```bash
export IMAGE=registry.example.com/dochub/dochub
export TAG=1.0.0

docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --tag "${IMAGE}:${TAG}" \
  --tag "${IMAGE}:latest" \
  --push \
  .
```

多架构构建使用 `--push` 直接推送到镜像仓库。`--load` 只适用于单一目标架构，不能加载多架构 manifest。

部署机更新镜像时，将 `.env` 中的 `DOCHUB_IMAGE` 指向目标版本，然后执行：

```bash
docker compose pull
docker compose up -d
docker compose ps
```

生产环境建议固定明确的版本标签，例如 `1.0.0`，避免不同机器直接使用 `latest` 时运行不同版本。

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
2. 输入项目名称、项目描述、SSH 仓库地址、一个或多个文档目录以及初始分支列表。项目描述会显示在项目列表中；文档目录示例为 `docs`、`design`，输入后按回车添加。
3. 系统异步 clone 每个分支并扫描 Markdown、`.html` 和 `.htm`。完成后项目卡片显示“已就绪”。
4. 进入项目后可切换分支、阅读文档、搜索当前分支并手动同步当前分支。
5. 在当前文档工具栏点击“下载”，可将该篇 Markdown 或 HTML 原文保存到本地。首版仅支持单篇下载，不提供批量或目录打包下载。
6. 项目创建者或管理员可在“项目管理”编辑仓库配置、添加或移除分支、删除项目。

项目分享路径使用 SSH/HTTP 仓库地址的最后一段，例如 `git@git.example.com:team/collie-server.git` 对应 `/projects/collie-server`。路径会统一转为小写并移除 `.git` 后缀；不同仓库最后一段重名时，创建项目会提示路径冲突。

左侧目录会分别展示每个已配置的文档根目录，显示 Markdown、HTML 文件和包含文档的目录。多个文档目录会合并到同一分支快照中，文件路径保留仓库内的完整目录前缀；目录不能重复，也不能互相包含。Markdown 的右侧目录提取二级至四级标题，并随滚动高亮当前位置；HTML 文档在隔离预览中独立滚动。

## 读取权限

项目默认私有，创建时或在“项目管理”中可切换公开／私有。公开仅表示所有已登录成员可读，未登录访客仍无法访问。
系统管理员可以在“管理后台 > 组织机构”中创建扁平组织机构并维护成员，也可以在“用户管理”中按用户设置所属组织。一个用户可以加入多个组织，
组织成员关系调整后立即影响其读取权限。项目创建人和管理员始终拥有完整权限，
私有项目的其他成员需要通过项目级或文档级授权获得读取权限：

- 项目级授权允许读取项目全部分支和全部文档，可以同时授权给用户和组织，在“项目管理”中配置。
- 文档级授权按仓库完整路径跨分支生效，可以同时授权给用户和组织，在文档页的“文档权限”中配置。
- 文档级授权同时允许读取该文档引用的图片、样式和脚本等资源，但不能读取其他未授权文档。
- 有文档级权限的成员可以在首页看到对应项目，但目录树、搜索和默认文档只包含已授权内容。
- 组织授权与个人直接授权取并集；用户退出组织后只失去由该组织带来的权限。
- 权限仅开放读取；项目编辑、分支管理、同步和授权管理仍只属于创建人和管理员。
- 切换公开／私有不会清除原有授权或触发重新同步；切回私有后继续按原有授权控制读取。

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

点击页头的个人头像可以修改密码。单点登录创建的用户初始没有本地密码，也可以在这里首次设置。
设置后可使用用户名和本地密码通过 HTTP Basic Auth 读取有权限的文档资源和下载文档，例如：

```bash
curl -u '{{{{PERSON_y2b6v52m}}}}:{{password}}' \
  'http://127.0.0.1:8080/api/projects/<项目 ID>/assets/docs/example.png?branch=main'
```

Basic Auth 只支持 `GET`、`HEAD` 读取请求，仍沿用项目可见范围和文档授权；写操作必须使用登录会话和 CSRF 校验。

### 个人 Agent 凭证

每个登录用户都可以点击个人头像，在“Agent 凭证”中创建只读令牌。令牌跟随创建者的当前项目权限，
用户权限变化会立即影响令牌，但不能用于登录 Web 或执行写操作。数据库使用哈希校验令牌，
并通过 AES-256-GCM 加密保存原始值，用户登录后可以再次查看自己的令牌；可以设置有效期并单独撤销。
调用时使用 Bearer 认证：

```bash
curl -H 'Authorization: Bearer <dochub_pat_token>' \
  'http://127.0.0.1:8080/api/projects/<项目 ID>/tree?branch=main'
```

个人 Agent 凭证只允许读取项目列表、项目详情、目录、文档、搜索、下载和附件。建议为每个 Agent
创建独立令牌，便于查看最近使用时间并单独撤销。
文档页提供“复制给 Agent”按钮，可直接复制读取当前 Markdown 或 HTML 原文的 `curl` 命令。

## 管理后台

管理员登录后可通过页头的设置图标进入独立的管理后台，使用左侧菜单在“用户管理”“组织机构”和“系统设置”之间切换；
左侧菜单支持收起为图标栏，窄屏下会自动使用图标栏。
用户管理支持查看、创建本地账号、调整成员／管理员角色，并可通过用户的“设置组织”入口配置多个所属组织。
组织机构页面支持创建组织、调整名称和成员。删除组织会级联移除其成员关系和全部项目、文档授权，
其他项目的个人归属和直接授权不受影响。系统设置用于管理单点登录站点。后台页面及接口仅管理员可访问，
普通成员会被引导回项目列表。

## OAuth2 单点登录

DocHub 使用统一的 OAuth2 授权码登录接入身份服务，与本地账号密码登录并存。
管理员登录后点击页头的设置图标进入“管理后台 > 系统设置”，可查看已配置站点列表并新增、编辑或删除站点；
配置保存在系统数据库，修改后立即生效，无须重启服务。配置接口仅管理员可访问。

公共入口由系统配置动态生成：`GET /api/auth/oauth2/:authKey/start` 和
`GET /api/auth/oauth2/:authKey/callback`，`GET /api/auth/options` 只返回公开的入口名称、地址和提示。
所有端点必须为无查询串、fragment 和用户凭据的 HTTP(S) 绝对地址；生产环境使用 HTTPS，
HTTP 只适合可信内网的过渡环境。

客户端密钥通过配置接口提交后，使用 AES-256-GCM 加密再写入数据库，读取配置时不会回显；
留空保存表示保持原密钥。该密钥使用独立文件加密，具体位置、备份和恢复要求见下文。

### 配置加密密钥

系统在首次保存需要加密的配置时，会在 `STORAGE_DIR` 下自动创建 `.dochub-config-key`。
该文件是 32 字节的随机密钥，权限为 `0600`，用于通过 AES-256-GCM 加密 OAuth2 客户端密钥
以及个人 Agent 凭证的原始值。密钥文件不保存到数据库，也不需要通过环境变量配置。

使用 Compose 时，容器内的 `STORAGE_DIR` 固定为 `/data`，宿主机上的默认位置是
`./data/.dochub-config-key`；如果设置了 `DATA_DIR`，实际位置为
`${DATA_DIR}/.dochub-config-key`。本地直接运行后端时，位置为
`${STORAGE_DIR}/.dochub-config-key`。可以执行以下命令确认文件：

```bash
ls -l ./data/.dochub-config-key
```

运行 DocHub 的系统用户必须能够读取该文件。不要手动修改文件内容，也不要将文件提交到 Git
或通过公开渠道传输。备份时应将 `.dochub-config-key` 与数据库备份一起保存，并分别限制访问权限。
如果只恢复数据库而没有恢复对应的密钥，已保存的 OAuth2 客户端密钥无法解密，需要重新填写；
已创建的 Agent 凭证也无法恢复原始令牌，需要重新创建。

### 配置方式

在配置页填写认证标识、显示名称、客户端 ID、客户端密钥、端点基础地址，以及授权、Token 和用户信息三个 path。
三个 path 必须以 `/` 开头，系统将其追加到基础地址；基础地址可以包含网关前缀，
例如 `https://identity.example.com/gateway` 加 `/oauth/token` 得到
`https://identity.example.com/gateway/oauth/token`。path 不允许填写完整 URL、查询串、fragment 或上级目录。
所有身份服务使用相同表单，不按厂商区分类型。每个站点同时具有内部稳定 ID 和对外认证标识：
内部 ID 用于保留账号绑定，认证标识用于生成 `/api/auth/oauth2/<认证标识>/start` 和
`/api/auth/oauth2/<认证标识>/callback`。认证标识仅允许小写字母、数字和连字符，例如 `collie`。
修改认证标识不会改变已有账号绑定，但需要同步更新身份服务登记的回调地址。
标准回调路径会随认证标识自动更新；自定义外部路径保持原样，需要反向代理转发至标准回调接口。
点击“自动填充”可恢复基于当前访问地址生成的默认值；
需要在对应身份服务端精确登记该完整地址。

可选固定签发方用于限制来源；未配置时使用可信用户信息端点返回的 `iss`，缺少 `iss` 时使用用户信息端点地址。
如需以后调整端点且保持账号绑定稳定，应配置固定签发方。
身份映射默认使用 `sub` 和 `name`，可通过点分路径读取嵌套字段；稳定用户 ID 支持字符串或数字，
数字保持原始精度。用户名仅用于展示，不能作为绑定键。
高级选项支持 Scope、`client_secret_post` / `client_secret_basic` 和可选 PKCE S256。
通用适配器不强制要求用户信息包含 `iss/aud`，也不处理 OIDC Discovery、ID Token 或 Refresh Token。

### 接入示例

Collie 的认证标识填 `collie`，端点基础地址填 `https://<Collie 域名>`，授权 path 填 `/collie/oauth2/authorize`，
Token path 填 `/collie/oauth2/token`，用户信息 path 填 `/collie/oauth2/userinfo`。
客户端 ID 使用 App.id；高级选项中的用户 ID 字段填 `sub`，用户名字段填 `userName`，
PKCE 保持关闭。将配置页显示的完整回调地址登记到 App 的 `redirectUris`，
`allowedOrigins` 登记 DocHub Origin，`responseTypes` 只配置 `["code"]`。
来源返回 `aud` 时校验其包含当前客户端 ID，返回 `iss` 时校验其与固定签发方及回调签发方一致。

用户须先在同一浏览器登录 Collie 门户，再点击 DocHub 的登录按钮；
未登录时 Collie 直接显示 401 `login_required`，不会回调 DocHub，此时登录门户后返回重新发起授权。
DocHub 不会自动循环重试。

认证标识设为 `collie` 后，登录入口和回调地址分别为 `/api/auth/oauth2/collie/start`、
`/api/auth/oauth2/collie/callback`。原有 `/api/auth/collie/start`、`/api/auth/collie/callback` 仍保留兼容入口。
旧版单站点配置会自动进入站点列表，同源完整端点地址会拆分为基础地址和三个 path，
并保留原回调地址、身份命名空间和账号绑定。删除站点会停止新登录，已有账号和授权不会删除。

### 扩展与会话

公共代码位于 `server/internal/oauth2`：`Provider` 定义授权入口和身份获取，
`Client` 复用授权码交换、Scope、PKCE、客户端认证和 HTTP 安全限制，
`Adapter` 负责来源特有的身份解析、Token 校验和登录提示。
普通身份服务通过端点与字段映射配置接入，API 路由、事务存储和前端无须增加厂商分支。

本地绑定键为 `provider_id + issuer + subject`，不同服务相同外部标识相互隔离。
首次登录自动创建无本地密码的普通成员，无现有文档权限；同名本地账号不会被自动认领。
首次建号时使用来源用户名作为本地登录名，重名时依次尝试 `_1`、`_2` 等数字后缀；
再次登录通过外部身份绑定查找原账号，已有登录名不变。
来源角色、管理员标记不转换为 DocHub 权限；不提供已有账号绑定、解绑和角色同步。
Access Token 只用于当次用户信息请求，不下发前端也不持久化。
默认本地会话 `SESSION_TTL=168h`，写请求继续校验 CSRF Token。
退出只清理 DocHub 会话，来源退出或应用禁用不立即撤销本地会话；当前没有统一单点退出。
移除某个服务配置仅停止新授权，已有本地会话继续有效。

本地前后端分离开发时使用前端同源的回调地址，例如 `http://127.0.0.1:5173/api/auth/oauth2/<认证标识>/callback`，
由 Vite `/api` 代理接收，避免登录事务 Cookie 或登录会话 Cookie 跨域。
应用不记录 OAuth2 回调访问日志；反向代理和错误监控也应禁止记录这些回调的查询串，
并禁止采集 Token 请求正文和 Authorization 请求头。

## 验证

后端测试：

```bash
make test
```

OAuth2 集成测试需要测试用 PostgreSQL，设置环境变量后会创建并清理隔离 schema，覆盖旧数据迁移、并发建号、
事务消费、SSO 会话及现有权限。不要使用生产数据库：

```bash
cd server
DOCHUB_TEST_DATABASE_URL='postgres://postgres:password@127.0.0.1:5432/dochub_test?sslmode=disable' go test ./...
```

未设置此变量时仅跳过数据库集成测试，配置和模拟 Collie HTTP 测试照常运行。

前端按照项目约定仅执行 TypeScript/SFC 语法检查：

```bash
make typecheck
```

建议的端到端验证仓库至少包含：

- `docs/README.md`、HTML 文档和多级子目录
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
- HTML 文档在禁止同源访问的 sandbox iframe 中运行，并叠加 CSP 限制脚本、表单、对象和顶层导航；预览资源通过短期签名凭证加载，并持续校验文档权限。
- 项目默认私有；公开项目允许所有已登录成员读取，私有项目按项目级、文档级授权控制读取。编辑、分支管理、同步和授权管理仅项目创建者或管理员可操作。
