# EggDoc 接入 Sub2API：配置示例与联调排障

更新日期：2026-10-26。本文整理 EggDoc 在 `testdoc.eggai.icu` 接入 Sub2API 生态接口时遇到的问题及解决方法。本次用户已确认登录和 EggAi 配置读取恢复正常。

文中域名、Client ID 是这次联调的历史示例，不代表所有部署的固定值。Client Secret 和 Session Secret 使用占位符；已废弃的历史密钥也不要重新启用，更不要把现用密钥提交到文档。

## 1. 先区分三个服务和三道授权检查

| 服务 | 本次示例 | 职责 |
| --- | --- | --- |
| EggDoc | `https://testdoc.eggai.icu` | 登录入口、加密 EggDoc Session、读取个性化配置 |
| Logto | `https://auth.eggai.icu/oidc` | 签发用户身份和 API Resource Access Token |
| 目标 Sub2API | `https://api.eggai.icu` | 验证用户 Access Token，返回用户、模型和 API Key |
| 本机另一套 Sub2API | `http://127.0.0.1:8080` | 独立部署；不能用它的配置推断公网 API 的生效值 |

本次排查时，公网 API 的 audience 为 `https://api.eggai.icu/api`，本机实例的 audience 为 `https://sub.eggai.icu/api`。必须以**接入目标实例**的配置为准，不要根据页面域名或本机配置猜测 audience。

登录成功只说明 Logto 身份认证和 EggDoc Session 成功。读取生态接口还需要：

1. Access Token 的签名、issuer、audience、有效期和接口 scope 验证通过。
2. Token 的 `client_id` 已被目标 Sub2API 的客户端白名单放行。
3. 同一 `(issuer, subject)` 已绑定到启用的 Sub2API 用户，且该用户有可用资源。

EggDoc 服务端携带的是指定 resource 的**用户 Access Token**，不是 ID Token、EggDoc Session Cookie、模型 API Key 或 M2M Token。

## 2. 读取目标实例的实际接入参数

从 EggDoc 容器网络检查：

```bash
cd ~/apps/EggDoc
docker compose exec -T app node --input-type=module -e '
const r = await fetch(new URL("/api/v1/ecosystem/docs", process.env.EGGDOC_EGGAI_ECOSYSTEM_URL));
const p = await r.json();
console.log(JSON.stringify({
  status: r.status,
  code: p.code,
  authentication: p.data?.authentication,
  sub2api_config: p.data?.sub2api_config
}, null, 2));
'
```

这是公开说明接口，不需要用户 Token。检查 `authentication.issuer`、`authentication.resource`、`sub2api_config["ecosystem.enabled"]` 和公开网关地址。未配置实例的文档可能返回示例占位值，不能把占位值当成已启用配置。不要使用 `docker inspect` 或 `docker compose config` 的完整输出提交工单，它们可能包含现用密钥。

## 3. Logto 控制台准备

为 EggDoc 创建独立的 **Traditional Web Application**，取得其 Client ID 和 Client Secret。本次历史 Client ID 示例：

```text
e3qhv659xqe2pa7fezshw
```

在该应用中注册精确回调地址：

```text
https://testdoc.eggai.icu/auth/callback
```

在 Logto 中创建或复用目标 API Resource，其 Identifier 必须与目标 Sub2API 的 audience 及 EggDoc 的 `EGGDOC_OIDC_RESOURCE` 完全相同。本次公网示例为：

```text
https://api.eggai.icu/api
```

API Resource 定义以下权限，再通过角色分配给需要接入的用户：

| scope | 接口 | EggDoc 当前是否调用 |
| --- | --- | --- |
| `ecosystem:me` | `GET /api/v1/ecosystem/me` | 是 |
| `ecosystem:models:read` | `GET /api/v1/ecosystem/models` | 是 |
| `ecosystem:tokens:read` | `GET /api/v1/ecosystem/keys` | 是 |
| `ecosystem:groups:read` | `GET /api/v1/ecosystem/groups` | 否，需要时再申请 |

EggDoc 同时请求 `openid profile offline_access`；其会话使用 refresh token，并在授权请求中加入 `prompt=consent`。若需要身份中的邮箱，可再申请 `email`。新增角色权限后，需要重新取得具有新 scope 的 Token，不能期待旧 Token 自动获得权限。仅增加 Sub2API 客户端白名单通常不需要重新登录。

## 4. EggDoc 的 .env 示例

以下适用于项目目录 `~/apps/EggDoc/.env`，不要提交真实 `.env`：

```dotenv
# EggDoc 的公开 Origin，不能带路径、查询参数或片段
EGGDOC_SITE_URL=https://testdoc.eggai.icu
PUBLIC_INSTALLER_ORIGIN=https://testdoc.eggai.icu

# EggDoc 自己的 Logto 应用
EGGDOC_OIDC_ISSUER=https://auth.eggai.icu/oidc
EGGDOC_OIDC_CLIENT_ID=e3qhv659xqe2pa7fezshw
EGGDOC_OIDC_CLIENT_SECRET=replace-with-your-eggdoc-client-secret
EGGDOC_OIDC_RESOURCE=https://api.eggai.icu/api
EGGDOC_OIDC_SCOPES="openid profile offline_access ecosystem:me ecosystem:models:read ecosystem:tokens:read"
EGGDOC_SESSION_SECRET=replace-with-a-random-32-byte-base64url-value

# 用户激活/管理入口和服务端生态接口根地址
EGGDOC_EGGAI_PLATFORM_URL=https://api.eggai.icu
EGGDOC_EGGAI_ECOSYSTEM_URL=https://api.eggai.icu

# 生成模型调用配置时的公开 Base URL，不是生态接口地址
PUBLIC_EGGAI_BASE_URL=https://api.eggai.icu/v1

# 使用 compose.vps.yaml 的镜像发布方式才读取此变量
EGGDOC_IMAGE=ghcr.io/lihuahua2336/eggdoc:1.6
```

`EGGDOC_IMAGE` 的 tag 是历史示例，部署时选择包含所需修复的实际版本；本地 `compose.yaml` 从源码构建 `eggdoc:local`，不会因为修改该变量而切换到 GHCR 镜像。

生成 Session Secret：

```bash
node -e "console.log(require('node:crypto').randomBytes(32).toString('base64url'))"
chmod 600 .env
```

将生成结果手动写入 `EGGDOC_SESSION_SECRET`。不要复用 Client Secret，不要使用全相同字符的示例值。Session Secret 变更会使已有 EggDoc Session 失效。

`EGGDOC_EGGAI_ECOSYSTEM_URL` 填服务端根地址；不要追加 `/v1` 或试图通过修改此变量给旧请求插入路径。EggDoc 请求使用以 `/` 开头的绝对路径，URL 中的路径前缀不会替换代码内的接口路径。

## 5. Sub2API 白名单：配置在哪里，什么优先生效

### 5.1 管理后台

已补齐的界面位于 **系统设置 → 安全设置 → Logto 生态适配层**。启用后可以编辑 issuer、audience、JWKS、公开网关及“允许的 Client ID（逗号分隔）”。

在**目标 API 实例**的白名单中保留其他应用的 ID，并追加 EggDoc 的 ID：

```text
原有ClientID1,原有ClientID2,e3qhv659xqe2pa7fezshw
```

输入 Client ID，不是 Client Secret，也不是替换 Sub2API 自身 OIDC 登录应用的 Client ID。保存后，运行时白名单会更新。

如果找不到输入框：本次发现旧前端虽然有变量、类型、翻译和保存参数，但缺少实际表单控件。源码已补齐；必须部署包含修复的新镜像，单独修改源码或重启旧镜像不会更新页面。

### 5.2 启动配置与环境变量

目标公网 API 的启动配置示例：

```yaml
ecosystem:
  enabled: true
  issuer_url: "https://auth.eggai.icu/oidc"
  audience: "https://api.eggai.icu/api"
  jwks_url: "https://auth.eggai.icu/oidc/jwks"
  allowed_client_ids:
    - "existing-application-client-id"
    - "e3qhv659xqe2pa7fezshw"
  public_gateway_url: "https://api.eggai.icu"
```

列表中的 `existing-application-client-id` 是占位符，替换为需保留的真实应用 ID。完整配置见 [config.example.yaml](../deploy/config.example.yaml)。

环境变量也支持逗号列表，但只写宿主机 `.env` 不会自动注入容器：

```dotenv
ECOSYSTEM_ALLOWED_CLIENT_IDS=existing-application-client-id,e3qhv659xqe2pa7fezshw
```

在 Sub2API 的 Compose service 内**合并**对应映射，勿覆盖现有 environment：

```yaml
services:
  sub2api:
    environment:
      ECOSYSTEM_ALLOWED_CLIENT_IDS: ${ECOSYSTEM_ALLOWED_CLIENT_IDS:?Set ECOSYSTEM_ALLOWED_CLIENT_IDS}
```

数据库中已保存的非空白名单优先于启动配置和环境变量。因此，旧部署只改环境变量，仍可能继续使用旧白名单。已有数据库设置时，优先通过管理后台更新。不要绕过客户端检查，也不要通过放宽 Token 验证来修复白名单配置错误。

## 6. EggDoc 适配的真实 Sub2API 响应

旧 EggDoc 调用 `/api/ecosystem/me`、`models`、`tokens`；Sub2API 使用 `/api/v1/ecosystem/me`、`models`、`keys`。仅修改根域名不够，还必须适配响应结构：

| 项目 | 旧契约 | Sub2API 契约 |
| --- | --- | --- |
| 成功响应 | `success: true` | `code: 0` |
| 模型名称 | 字符串或 `id` | 对象内的 `model` |
| Key 列表 | `data` 数组 | `data.items`，带 `page/pages` 等分页 |
| Key 字段 | `token_id/api_key/token_name/group` | `id/key/name/group_id` |
| Key Base URL | 可能返回 `base_url` | 当前不返回，使用 `PUBLIC_EGGAI_BASE_URL` |
| 未分组 | 原接口的分组名 | `group_id: null`，EggDoc 展示“未分组” |

分页返回示例，Key 为不可用的占位值：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "items": [{
      "id": 101,
      "name": "EggDoc example",
      "key": "sk-EXAMPLE-DO-NOT-USE",
      "group_id": 1,
      "expires_at": null,
      "is_expired": false,
      "quota": 0,
      "quota_used": 0,
      "is_quota_exhausted": false
    }],
    "total": 1,
    "page": 1,
    "page_size": 100,
    "pages": 1
  }
}
```

EggDoc 会读取全部 Key 分页，并排除明确标记为过期或额度耗尽的 Key。模型名称去重。当前显示的是分组 ID，不应假定 Key 接口返回分组名称。

## 7. 401、403 与本次实际故障

本次症状是 `/api/auth/user` 返回 `authenticated: true`，但 `/api/eggai/account` 返回 401。旧 EggDoc 将上游所有 401/403 都映射成重新授权，并清除了自己的 Session，掩盖了真正原因。

修复后，目标 Sub2API 返回的真实原因为：

```json
{
  "state": "temporary-error",
  "reason": "ecosystem_client_not_allowed"
}
```

最终处理是将 EggDoc 的 Logto Client ID 加入**实际被调用的公网 API 实例**的白名单。反复重建 EggDoc、更新本机另一套 Sub2API 或更换 Client Secret 都不能替代这个操作。

| 现象 / reason | 检查和解决方法 |
| --- | --- |
| EggDoc `401 anonymous` | 没有有效 EggDoc Session；重新登录，检查域名、Secure Cookie 和 Session Secret |
| 上游 `401 invalid_logto_token` | 检查 resource/audience、issuer、签名、类型和过期时间；不能传 ID Token 或本地 Session |
| `403 ecosystem_client_not_allowed` | 检查 Token 的 client_id；在目标实例白名单保留已有 ID 并追加本应用 ID |
| `403 insufficient_scope` | 检查 API Resource 权限、用户角色及请求 scopes；重新获取有权限的 Token |
| `403 ecosystem_identity_not_linked` | 用户先通过相同 Logto issuer 登录或绑定 Sub2API；EggDoc 将其显示为待激活，保留登录 |
| `403 ecosystem_user_unavailable` | 检查目标 Sub2API 用户是否被禁用或删除 |
| `403 ecosystem_disabled` | 启用目标实例的生态适配层 |
| `502 temporary-error` | 检查网络、接口路径、`code: 0`、Key 分页和字段结构，不要仅检查 HTTP 200 |
| 回调地址不允许 | 在 EggDoc 的 Logto 应用注册精确的 `https://testdoc.eggai.icu/auth/callback` |
| 更改代码后页面仍无配置项 | 检查构建的新镜像是否已替换运行容器；必要时强制刷新浏览器 |
| 本机修复了但公网仍 403 | 确认请求根地址、目标实例及配置来源，不能把“本机容器更新”当成“公网 API 已更新” |

上游 403 不应清除有效登录。当前 EggDoc 对普通权限拒绝保留 Session，以 HTTP 403 返回 `temporary-error` 和允许输出的 `reason`。日志只记录请求路径、HTTP 状态和允许输出的原因，不记录 Token、Cookie 或 Key。

## 8. 容器、域名与验收

### 8.1 更新 EggDoc

```bash
cd ~/apps/EggDoc
docker compose config --quiet
docker compose up -d --build app
docker compose ps
curl -fsS http://127.0.0.1:4322/api/health
```

健康响应为 `{"status":"ok"}`。仅更新运行时 Secret 时，可执行 `docker compose up -d --force-recreate app` 重新注入变量；普通 `docker compose restart` 不会更新容器环境。修改源码、`EGGDOC_SITE_URL` 或 `PUBLIC_*` 后需要重新构建。

可只输出镜像标识，确认运行容器确实使用刚构建的镜像；本地构建方式下两者应一致：

```bash
docker inspect eggdoc-app-1 --format '{{.Image}}'
docker image inspect eggdoc:local --format '{{.Id}}'
```

如果使用 GHCR 发布镜像，应使用项目的 `compose.vps.yaml` / VPS 部署流程；不要以为本地 Compose 的 `EGGDOC_IMAGE` 变量会覆盖 `eggdoc:local`。

### 8.2 更新本机 Sub2API

本次新增本地源码构建覆盖文件，项目根目录执行：

```bash
cd ~/apps/sub2api
docker compose -p deploy \
  -f deploy/docker-compose.prod.yml \
  -f deploy/docker-compose.local-build.yml \
  --env-file deploy/.env \
  up -d --build --no-deps sub2api

curl -fsS http://127.0.0.1:8080/health
```

该命令适用于本次 `deploy` 项目拓扑，只替换应用 service；迁移其他部署时先核对项目名、命名卷和网络。已有手工创建的同名容器时，应先备份、停止并改名保留，再交给 Compose 管理，不要删除数据卷来解决命名冲突。

部署前备份数据库并保留旧镜像。本次本地镜像名为 `sub2api:local-ecosystem`，服务端嵌入编译后的前端，所以新增界面需要重建整个镜像。

### 8.3 HTTPS 与回调

本次 Nginx 将 `testdoc.eggai.icu` 的 HTTPS 请求反代到 `127.0.0.1:4322`，HTTP 重定向到 HTTPS。保持应用端口只绑定回环地址。在证书签发前使用 HTTP ACME 配置，证书存在后再启用 HTTPS 配置；每次 reload 前先执行 `nginx -t`。本次部署配置见 EggDoc 项目 `deploy/nginx/testdoc.eggai.icu.conf`。

`EGGDOC_SITE_URL`、`PUBLIC_INSTALLER_ORIGIN` 和浏览器访问的域名使用 `https://testdoc.eggai.icu`；`PUBLIC_EGGAI_BASE_URL` 仍是模型 API 地址，不要一起改成文档站域名。

### 8.4 分层验收

1. 检查目标 API 的公开 `/api/v1/ecosystem/docs`，确认实际 issuer/resource 和启用状态。
2. 检查 EggDoc 与 Sub2API 容器的运行镜像及健康状态，不能只确认 build 成功。
3. 检查 EggDoc 的 `/auth/login` 重定向中回调、resource 和 scopes；不要保存或分享完整授权 URL、Cookie。
4. 用测试用户登录，确认 `/api/auth/user` 为已登录，再确认 `/api/eggai/account` 为 HTTP 200、`state: active`。
5. 确认模型和 Key 列表可用、多 Key 可以切换；只记录结果，不打印响应中的真实 Key。
6. 若失败，只记录 HTTP 状态、`state` 和 `reason`，按上表定位到身份、权限、资源或部署层。

本次已通过 16 项相关浏览器集成测试，以及分页和空分组的定向检查。此前测试 mock 只模拟旧 `success: true` 和 `data` 数组，因此“测试通过”未能证明真实 Sub2API 契约兼容；现在 mock 已使用 `code: 0` 和 Key 分页，后续修改应继续覆盖真实格式。

## 9. 新应用最短接入清单

- [ ] 确认目标 API 根地址及其实际 audience，区分本机和公网部署。
- [ ] 创建自己的 Logto Traditional Web 应用并注册精确回调。
- [ ] 用户已分配所需 API Resource scopes；应用请求 resource 和 scopes。
- [ ] 将本应用 Client ID 加入目标 Sub2API 白名单，保留其他应用。
- [ ] 用户已通过同一 issuer 绑定到启用的 Sub2API 用户。
- [ ] 配置业务项目实际读取的变量名，不混用 `EGGDOC_*` 和其他项目的 `LOGTO_*`。
- [ ] 适配 `/api/v1/ecosystem/*`、成功信封、分页及字段映射。
- [ ] 构建并替换正确项目的运行容器，分别验证健康、登录和资源读取。
- [ ] 所有现用密钥仅存在于部署环境；文档只放占位符和非敏感示例。
