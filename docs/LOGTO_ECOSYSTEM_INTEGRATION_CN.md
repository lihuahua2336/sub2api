# Logto 生态适配层配置与应用接入指南

本文说明如何使用 Logto 用户 Access Token 调用 sub2api 的只读生态接口，并以当前生产拓扑和 `infinite-canvas` 接入实现为参考模板。

本文面向部署人员和接入其他应用的开发者。Token 声明和验签依据详见 [Logto 生态 Access Token 契约](logto-ecosystem-token-contract.md)。

## 1. 架构与对象关系

Logto 中需要区分两个对象：

| 对象 | Logto 类型 | 当前示例 | 用途 |
| --- | --- | --- | --- |
| sub2api | API Resource | `https://sub.eggai.icu/api` | Access Token 的 audience，由 sub2api 验签并提供资源 |
| infinite-canvas 或其他前端/BFF | Traditional Web 应用 | Client ID `k26euxjmqraz9vt5chzq1` | 发起用户登录，为指定 API Resource 申请 Access Token |

对于自有的服务端 Web 应用，应创建 **Traditional Web（传统网页应用）**，不要把 sub2api 创建成登录客户端，也不要把自有应用误建成第三方应用。第三方应用适用于外部开发者集成，会增加权限申请和用户授权约束。

请求链路如下：

```text
用户浏览器
  -> 业务应用（Traditional Web）
  -> Logto 用户授权（resource + scopes）
  <- 用户 Access Token
业务应用服务端
  -> sub2api /api/v1/ecosystem/*
     Authorization: Bearer <Logto Access Token>
```

sub2api 不接受业务应用自己的 Session JWT，也不接受 `client_credentials`/M2M Token 调用这些用户资源接口。

## 2. Logto 控制台配置

### 2.1 创建 API Resource

在 Logto 中创建 API Resource：

```text
名称：sub2api
API Identifier：https://sub.eggai.icu/api
```

Identifier 必须与 sub2api 的 `Audience` 以及客户端请求的 resource 完全一致，包括协议、路径和末尾斜杠。

为该 API Resource 创建以下权限：

| Scope | sub2api 接口 | 说明 |
| --- | --- | --- |
| `ecosystem:me` | `GET /api/v1/ecosystem/me` | 当前已绑定的 sub2api 用户 |
| `ecosystem:groups:read` | `GET /api/v1/ecosystem/groups` | 用户可用分组 |
| `ecosystem:models:read` | `GET /api/v1/ecosystem/models` | 用户可用模型 |
| `ecosystem:tokens:read` | `GET /api/v1/ecosystem/keys` | 用户自己的有效 API Key，响应禁止缓存 |

### 2.2 配置 Role 和用户权限

创建一个角色，例如 `sub2api-ecosystem-user`，将上述四个 API Resource 权限加入角色，再把角色分配给需要接入的 Logto 用户。

客户端请求 scope 不等于用户自动拥有权限。只有用户已经被授予对应权限，Logto 才会把这些 scope 写入用户 Access Token。

### 2.3 创建业务应用

为每个接入应用创建独立的 Traditional Web 应用。以 infinite-canvas 为例：

```text
应用类型：Traditional Web
Redirect URI：https://canvas.eggai.icu/callback
Post sign-out redirect URI：https://canvas.eggai.icu/
```

记录该应用的 Client ID 和 Client Secret。Client Secret 只能保存在服务端 Secret 或环境变量中，不能进入浏览器代码、镜像仓库或文档。

每增加一个应用，还需要把它的 Client ID 加入 sub2api 的 `Allowed Client IDs`。建议每个应用使用独立 Client ID，方便撤销、审计和限流。

## 3. sub2api 配置

可以在 sub2api 管理后台启用“Logto 生态适配层”，当前生产配置为：

| 配置项 | 当前值 |
| --- | --- |
| 启用 | 是 |
| Issuer URL | `https://auth.eggai.icu/oidc` |
| Audience | `https://sub.eggai.icu/api` |
| JWKS URL | `https://auth.eggai.icu/oidc/jwks` |
| Allowed Client IDs | `i1hyad0o6d7zc246skho9,k26euxjmqraz9vt5chzq1` |
| Public Gateway URL | `https://sub.eggai.icu` |
| Allowed Signing Algorithms | `RS256,ES256,PS256,ES384` |
| Clock Skew | `60` 秒 |
| JWKS Request Timeout | `5` 秒 |
| JWKS Max Response Bytes | `100000` |
| JWKS Cache TTL | `300` 秒 |
| JWKS Minimum Refresh Interval | `30` 秒 |
| Rate Limit | `60` 次/分钟 |

等价 YAML 配置示例：

```yaml
ecosystem:
  enabled: true
  issuer_url: "https://auth.eggai.icu/oidc"
  audience: "https://sub.eggai.icu/api"
  jwks_url: "https://auth.eggai.icu/oidc/jwks"
  allowed_client_ids:
    - "i1hyad0o6d7zc246skho9"
    - "k26euxjmqraz9vt5chzq1"
  public_gateway_url: "https://sub.eggai.icu"
  allowed_signing_algs: ["RS256", "ES256", "PS256", "ES384"]
  clock_skew_seconds: 60
  jwks_request_timeout_seconds: 5
  jwks_max_response_bytes: 100000
  jwks_cache_ttl_seconds: 300
  jwks_refresh_min_interval_seconds: 30
  rate_limit_per_minute: 60
```

生产环境最好只允许 Logto 实际使用的签名算法。例如确认租户只签发 RS256 后，将列表收紧为 `["RS256"]`。

### 3.1 用户身份绑定前提

Access Token 验证成功后，sub2api 会用经过验证的 `(iss, sub)` 查询本地 OIDC 身份：

```text
provider_type = oidc
provider_key = Token 的 iss
provider_subject = Token 的 sub
```

因此用户必须先通过相同 Logto issuer 登录或绑定 sub2api，并且本地用户处于启用状态。不能使用邮箱替代该绑定，也不能因为两个系统中的邮箱相同就认为已经关联。

## 4. 业务应用环境变量

当前 infinite-canvas 的脱敏模板如下：

```dotenv
APP_PUBLIC_URL=https://canvas.eggai.icu
COOKIE_SECURE=true
SESSION_SECRET=<至少-32-字符的随机值>

LOGTO_ISSUER=https://auth.eggai.icu/oidc
LOGTO_INTERNAL_ISSUER=
LOGTO_CLIENT_ID=k26euxjmqraz9vt5chzq1
LOGTO_CLIENT_SECRET=<Traditional-Web-应用的-Client-Secret>
LOGTO_SCOPE=openid profile email

NEW_API_BASE_URL=https://sub.eggai.icu
NEW_API_PUBLIC_URL=https://sub.eggai.icu
NEW_API_DISPLAY_NAME=EggAi
NEW_API_LOGTO_AUDIENCE=https://sub.eggai.icu/api
NEW_API_LOGTO_SCOPE=ecosystem:me ecosystem:models:read ecosystem:tokens:read ecosystem:groups:read
```

变量职责：

| 变量 | 含义 |
| --- | --- |
| `LOGTO_ISSUER` | Logto issuer；当前 SDK 会移除末尾 `/oidc` 后作为 Logto endpoint |
| `LOGTO_INTERNAL_ISSUER` | 可选的容器内部 Logto 地址；为空时使用 `LOGTO_ISSUER` |
| `LOGTO_CLIENT_ID/SECRET` | 当前业务应用的 Traditional Web 凭据 |
| `LOGTO_SCOPE` | OIDC 基础 scopes |
| `NEW_API_LOGTO_AUDIENCE` | 请求 Access Token 时使用的 resource，必须等于 sub2api Audience |
| `NEW_API_LOGTO_SCOPE` | sub2api 生态接口 scopes |
| `NEW_API_BASE_URL` | 服务端实际请求 sub2api 的地址 |
| `NEW_API_PUBLIC_URL` | 返回给浏览器使用的公开网关地址 |

修改这些变量后需要重新构建或至少重启读取环境变量的应用进程，并退出 Logto、清理旧会话后重新授权。旧 Access Token 不会因为后台新增 Role 或 scope 而自动增加权限。

## 5. 客户端实现要求

### 5.1 登录时同时请求 resource 和 scopes

以 `@logto/next` 为例：

```ts
import type { LogtoNextConfig } from "@logto/next";

const resource = "https://sub.eggai.icu/api";
const config: LogtoNextConfig = {
  endpoint: "https://auth.eggai.icu",
  appId: process.env.LOGTO_CLIENT_ID!,
  appSecret: process.env.LOGTO_CLIENT_SECRET!,
  baseUrl: "https://your-app.example.com",
  cookieSecret: process.env.SESSION_SECRET!,
  cookieSecure: true,
  scopes: [
    "openid",
    "profile",
    "email",
    "ecosystem:me",
    "ecosystem:groups:read",
    "ecosystem:models:read",
    "ecosystem:tokens:read",
  ],
  resources: [resource],
};
```

仅配置 scopes 而不配置 resource，通常只能得到面向 UserInfo/OIDC 的 Token；仅配置 resource 而用户没有对应 Role 权限，也不会得到所需 scopes。

### 5.2 为指定 resource 获取 Access Token

```ts
import { getAccessToken, getLogtoContext } from "@logto/next/server-actions";

const context = await getLogtoContext(config);
if (!context.isAuthenticated) {
  throw new Error("Logto session expired");
}

const accessToken = await getAccessToken(
  config,
  "https://sub.eggai.icu/api",
);
```

`getLogtoContext()` 中的 ID Token claims 用于识别登录用户；`getAccessToken(config, resource)` 返回的资源 Access Token 才能发送给 sub2api。不要发送应用自己签发的本地 Session JWT。

### 5.3 调用 sub2api

```ts
async function getEcosystem<T>(accessToken: string, path: string): Promise<T> {
  const response = await fetch(`https://sub.eggai.icu${path}`, {
    headers: { Authorization: `Bearer ${accessToken}` },
    cache: "no-store",
  });

  const payload = await response.json();
  if (!response.ok || payload.success === false) {
    throw new Error(payload.message || `sub2api returned ${response.status}`);
  }
  return ("data" in payload ? payload.data : payload) as T;
}

const models = await getEcosystem(accessToken, "/api/v1/ecosystem/models");
const keys = await getEcosystem(accessToken, "/api/v1/ecosystem/keys");
```

这些调用应放在业务应用服务端。特别是 `/keys` 会返回用户自己的可用 API Key，不应把 Logto Access Token 暴露到浏览器，也不要缓存该响应。

### 5.4 当前参考实现位置

infinite-canvas：

| 文件 | 职责 |
| --- | --- |
| `web/src/lib/eggai-server.ts` | 合并 OIDC/生态 scopes，配置 `resources` 和 Traditional Web 凭据 |
| `web/src/app/api/auth/sign-in/route.ts` | 发起 Logto 登录和回调跳转 |
| `web/src/app/api/eggai/config/route.ts` | 检查会话并按 audience 调用 `getAccessToken` |
| `web/src/services/api/new-api-server.ts` | 将 Logto Access Token 放入 Bearer header，读取 models 和 keys |

sub2api：

| 文件 | 职责 |
| --- | --- |
| `backend/internal/server/routes/ecosystem.go` | 注册 `/api/v1/ecosystem/*` 路由 |
| `backend/internal/handler/ecosystem_handler.go` | 定义各接口所需 scope、授权与限流 |
| `backend/internal/service/ecosystem_token_verifier.go` | JWT、JWKS、issuer、audience、client 和 scope 校验 |
| `backend/internal/repository/ecosystem_repo.go` | 按 `(issuer, subject)` 查询本地身份和用户资源 |

## 6. sub2api 的 Token 校验规则

sub2api 会按顺序检查：

1. JOSE header `typ` 必须是 `at+jwt`，且必须有 `kid`。
2. 签名算法必须在允许列表中，签名密钥来自配置的 JWKS URL。
3. `iss` 和 `aud` 必须与配置精确匹配。
4. Token 必须有有效的 `exp` 和非空 `sub`；存在 `iat`、`nbf` 时必须通过时间校验。
5. `client_id` 必须存在，并在 Allowed Client IDs 中。
6. 拒绝 `gty=client_credentials` 或 `sub == client_id` 的机器 Token。
7. `scope` 必须以空格分隔并精确包含当前接口要求的 scope。
8. `(iss, sub)` 必须已绑定到一个启用的 sub2api 用户。
9. 请求通过后，再应用生态层限流和资源所有权过滤。

Scope 是完整值匹配。例如 `prefix-ecosystem:me-suffix` 不会被当作 `ecosystem:me`。

## 7. 部署验收

### 7.1 检查 discovery 和 JWKS

```bash
curl -fsS https://auth.eggai.icu/oidc/.well-known/openid-configuration
curl -fsS https://auth.eggai.icu/oidc/jwks
```

确认 discovery 中的 issuer 精确等于 `https://auth.eggai.icu/oidc`，JWKS 可从 sub2api 容器网络访问。

### 7.2 检查 Access Token claims

只在受控调试环境解码 Token，不要把完整 Token 写入日志或提交到工单。应至少确认：

```json
{
  "iss": "https://auth.eggai.icu/oidc",
  "aud": "https://sub.eggai.icu/api",
  "sub": "<Logto user subject>",
  "client_id": "<业务应用 Client ID>",
  "scope": "openid profile email ecosystem:me ecosystem:groups:read ecosystem:models:read ecosystem:tokens:read"
}
```

并确认 JOSE header 为：

```json
{
  "typ": "at+jwt",
  "kid": "<JWKS 中存在的 key id>",
  "alg": "RS256"
}
```

### 7.3 使用实际用户 Token 验收接口

```bash
export SUB2API_LOGTO_ACCESS_TOKEN='<临时用户 Access Token>'

curl -fsS \
  -H "Authorization: Bearer ${SUB2API_LOGTO_ACCESS_TOKEN}" \
  https://sub.eggai.icu/api/v1/ecosystem/me

curl -fsS \
  -H "Authorization: Bearer ${SUB2API_LOGTO_ACCESS_TOKEN}" \
  https://sub.eggai.icu/api/v1/ecosystem/models
```

验收结束后清除 shell 变量：

```bash
unset SUB2API_LOGTO_ACCESS_TOKEN
```

## 8. 常见错误

| HTTP/原因 | 含义 | 优先检查 |
| --- | --- | --- |
| `401 invalid_logto_token` | 不是有效的资源 Access Token | 是否误传 ID Token/本地 Session JWT；`typ`、签名、issuer、audience、过期时间 |
| `403 insufficient_scope` | Token 有效但缺少接口 scope | Logto API Resource scopes、Role、用户授权、客户端是否同时请求 resource 和 scope、是否仍使用旧 Token |
| `403 ecosystem_client_not_allowed` | `client_id` 不在白名单或缺失 | 将业务应用 Client ID 加入 sub2api Allowed Client IDs |
| `403 ecosystem_identity_not_linked` | `(iss, sub)` 未绑定 sub2api 用户 | 让用户先通过同一 Logto issuer 登录/绑定 sub2api |
| `403 ecosystem_user_unavailable` | 本地用户禁用或删除 | sub2api 用户状态 |
| `429 RATE_LIMITED` | 超过生态层限流 | `rate_limit_per_minute` 与调用频率 |
| `503 ecosystem_unavailable` | JWKS 或内部依赖不可用 | JWKS 网络、超时、响应大小、数据库/服务状态 |

## 9. 新应用接入清单

- [ ] 在 Logto 创建 Traditional Web 应用，不复用其他应用的 Client Secret。
- [ ] 配置准确的 Redirect URI 和 Post sign-out URI。
- [ ] 确认 sub2api API Resource Identifier 为 `https://sub.eggai.icu/api`。
- [ ] 为用户分配包含所需 `ecosystem:*` 权限的 Role。
- [ ] 客户端登录配置同时包含 resource 和 scopes。
- [ ] 服务端使用 `getAccessToken(config, resource)` 获取资源 Access Token。
- [ ] sub2api Allowed Client IDs 中加入新应用 Client ID。
- [ ] 用户已在 sub2api 建立相同 `(issuer, sub)` 的 OIDC 身份绑定。
- [ ] 重启应用并清除旧会话后重新授权。
- [ ] 分别验证 `/me`、`/models` 和应用实际需要的其他接口。
- [ ] 日志中不记录 Access Token、Client Secret 或返回的 API Key。

## 10. 凭据安全

Client Secret、Session Secret、Logto Access Token 和 sub2api API Key 都属于敏感凭据。若曾出现在聊天、日志、截图或 Git 历史中，应立即在 Logto 或对应系统中轮换，而不是仅从文件中删除。
