# Logto 生态 access token 契约研究

状态：Issue #1 实现前置研究。本文固定可复核的上游版本，并区分标准要求、Logto 源码事实和本项目的受信部署约定。

## 版本与本仓库边界

仓库没有锁定某个 Logto 镜像、租户版本或部署 commit；`deploy/config.example.yaml` 只提供 issuer/JWKS/audience 的示例。因此不能把下面的版本当作目标租户已经运行的版本。作为实现时的可复核参考，采用 Logto 官方 release `v1.43.0`，commit [`d066df7d26d596b6ba7ad0bdfaaecfda9c612226`](https://github.com/logto-io/logto/commit/d066df7d26d596b6ba7ad0bdfaaecfda9c612226)（2026-08-31）；该版本的 `packages/core/package.json` 锁定官方 fork `node-oidc-provider` commit [`513c523c0e68ee6112da8c871cce86204a136163`](https://github.com/logto-io/logto/commit/513c523c0e68ee6112da8c871cce86204a136163)。上线前仍需把目标租户的实际 Logto 版本、issuer 和 JWKS 与此证据重新比对。

## 已核实的 token 结构

### 用户 resource access token

Logto v1.43.0 的资源服务器默认使用 JWT；`getSharedResourceServerData()` 明确返回 `accessTokenFormat: 'jwt'`，并使用租户配置的签名算法：[`resource.ts#L15-L22`](https://github.com/logto-io/logto/blob/d066df7d26d596b6ba7ad0bdfaaecfda9c612226/packages/core/src/oidc/resource.ts#L15-L22)。JWT access-token formatter 在固定的 `node-oidc-provider` fork 中生成以下关键声明：

* `sub`：有用户账户时来自 `accountId`（可按客户端 subject type 做 pairwise 转换）；
* `client_id`：请求 token 的 OAuth client ID；
* `scope`：Logto 特意保持存在的字符串；
* `iss`：provider issuer；`aud`：资源服务器 audience；`iat`、`exp`、`jti`：生命周期/唯一标识；
* JOSE header `typ`：`at+jwt`；非对称签名时带 `kid`。

源码依据：[`formats/jwt.js#L96-L163`](https://github.com/logto-io/node-oidc-provider/blob/513c523c0e68ee6112da8c871cce86204a136163/lib/models/formats/jwt.js#L96-L163)、[`access_token.js#L9-L31`](https://github.com/logto-io/node-oidc-provider/blob/513c523c0e68ee6112da8c871cce86204a136163/lib/models/access_token.js#L9-L31)。

因此，Issue #1 所需的“客户端声明名称”是 **`client_id`**，其格式是非空 JSON 字符串，值为签发该 token 的 OAuth client identifier。不是 `clientId`，也不能把 `azp` 当作别名。资源 token 的 `aud` 是 API resource indicator，不是前端 client ID；Logto 的 provider 配置将 resource server 的 `audience/indicator`写入 token：[`resource.ts#L15-L22`](https://github.com/logto-io/logto/blob/d066df7d26d596b6ba7ad0bdfaaecfda9c612226/packages/core/src/oidc/resource.ts#L15-L22)。

### 用户 token 与 client-credentials/M2M token

Logto 的实现把两者作为不同的 oidc-provider 实体：用户 token 是 `AccessToken`，机器 token 是 `ClientCredentials`；自定义 claim 逻辑也用 `instanceof` 分支处理二者，并只为 `AccessToken` 查询用户上下文：[`extra-token-claims.ts#L160-L235`](https://github.com/logto-io/logto/blob/d066df7d26d596b6ba7ad0bdfaaecfda9c612226/packages/core/src/oidc/extra-token-claims.ts#L160-L235)。

在 JWT formatter 中，`sub` 取 `accountId`；若没有账户主体则回退为 `clientId`：[`formats/jwt.js#L99-L130`](https://github.com/logto-io/node-oidc-provider/blob/513c523c0e68ee6112da8c871cce86204a136163/lib/models/formats/jwt.js#L99-L130)。因此，在该固定实现下：

* 用户 resource token：`sub` 是 Logto 用户 subject（可能是 pairwise subject），且应与 `client_id` 不同；
* client-credentials/M2M token：`sub` 回退为该应用的 `client_id`，因此 `sub == client_id` 是强信号；
* 两类 token 都有 `client_id`、`aud`、`scope` 和生命周期声明；仅凭 `client_id` 存在不能证明是用户 token。

`gty` 不是可靠的唯一分类依据：`AccessToken` 的模型 mixin 会把 `gty` 放入内部 payload，但 JWT formatter 的公开 payload 选择没有把 `gty` 固定写入；`ClientCredentials` 也共享相同 formatter。源码依据：[`has_grant_type.js#L1-L8`](https://github.com/logto-io/node-oidc-provider/blob/513c523c0e68ee6112da8c871cce86204a136163/lib/models/mixins/has_grant_type.js#L1-L8)、[`client_credentials.js#L6-L18`](https://github.com/logto-io/node-oidc-provider/blob/513c523c0e68ee6112da8c871cce86204a136163/lib/models/client_credentials.js#L6-L18)（链接中的 commit 路径如需访问，请使用同一 SHA 的 `client_credentials.js`）。

Logto 自身的认证中间件在同一 release 中采用 `sub === client_id` 判定 app、否则判定 user；这是受信签发约定而非 JWT/OAuth 通用规则：[`koa-auth/index.ts#L57-L72`](https://github.com/logto-io/logto/blob/d066df7d26d596b6ba7ad0bdfaaecfda9c612226/packages/core/src/middleware/koa-auth/index.ts#L57-L72)。

### 标准 claim 的语义和格式

* RFC 9068 要求 JWT access token 的 `iss`、`exp`、`aud`、`sub`、`client_id`、`iat`、`jti`，并建议 `typ=at+jwt`；对涉及资源所有者的授权，`sub` 应对应资源所有者，对 client-credentials 则应对应表示客户端应用的标识：[`RFC 9068 §2.2`](https://www.rfc-editor.org/rfc/rfc9068.html#section-2.2)。
* RFC 8693 §4.2 规定 `scope` 是以空格分隔的 scope 字符串；§4.3 规定 `client_id` 是请求该 token 的 OAuth client identifier：[`RFC 8693 §4.2`](https://www.rfc-editor.org/rfc/rfc8693.html#section-4.2)、[`RFC 8693 §4.3`](https://www.rfc-editor.org/rfc/rfc8693.html#section-4.3)。
* RFC 7519 将 `iss`、`sub`、`aud` 定义为签发者、主体和受众；`exp` 是过期时间，`nbf` 是生效前时间，`iat` 是签发时间，值为 NumericDate。RFC 7519 本身把这些 claim 标为可选，具体是否必需由应用 profile 决定：[`RFC 7519 §4.1`](https://www.rfc-editor.org/rfc/rfc7519.html#section-4.1)。

Logto v1.43.0 的资源 JWT formatter 使用 `iss`、`aud`、`iat`、`exp`、`sub`、`client_id` 和 `scope`；源码没有把 `nbf` 作为每个 token 的固定输出字段。因此验证器应在**存在时**校验 `nbf`，并按本项目实现决定是否要求 `iat`/`exp`（Issue #1 已要求必须有 `exp`、校验存在的 `nbf`/`iat`）。

## 本项目应采用的受信签发约定

标准声明不能单独证明“这是用户 token”：RFC 9068 只规定 `sub` 在不同 grant 下应如何解释，并没有一个跨实现、必然存在的 `user_token=true` 或 `grant_type` claim。即使 `sub != client_id`，也只能作为目标 Logto 版本行为下的防御性检查，不能替代 issuer、签名、audience、scope 和客户端白名单。

因此生态适配层应固定以下部署约定，并把它们视为本服务的信任边界：

1. 精确匹配配置的 `issuer_url`，只信任该 issuer 的 JWKS；限制签名算法和 `kid`，拒绝 ID token（要求 `typ=at+jwt` 或等价的明确资源-token profile 检查）。
2. 精确匹配配置的 API resource `audience`；它不是 client ID。
3. 要求 `exp`、非空字符串 `sub`、非空字符串 `client_id`，并按空格分隔后做完整 scope 匹配。
4. 仅允许配置白名单中的 `client_id`；白名单不等于用户 token 类型验证。
5. 对目标 Logto v1.43.0 契约，拒绝 `sub == client_id` 的 token（这是该版本 M2M fallback 的直接结果），并拒绝显式 `gty=client_credentials`；若未来目标版本改变 M2M subject 规则，必须重新核实并更新契约，而不能静默放宽。
6. 通过已验证的 `(issuer, sub)` 查询本地 `oidc` 身份绑定；不使用邮箱、调用方传入的 user ID 或 `client_id` 推断资源归属。

未能从标准或固定 Logto 源码证明的事项：目标租户是否运行 v1.43.0、是否启用自定义 JWT claims、是否使用 pairwise subject、具体 resource indicator/audience 值、签名算法/JWKS 轮换策略，以及租户是否会通过自定义 claims 改写 `sub`/`client_id`。这些必须在部署验收时通过目标租户的 discovery、JWKS、测试用户授权 token 和 client-credentials token 实测确认。

## 验收用最小 token 矩阵

| token | 必须满足 | 必须拒绝 |
| --- | --- | --- |
| 用户 resource token | 签名有效；`iss`/`aud` 精确；`typ=at+jwt`；`exp`/非空 `sub`/`client_id`；白名单 client；所需 scope；`sub != client_id`；绑定 `(issuer, sub)` 存在且用户 active | ID token、错误 issuer/audience、过期/未生效、缺 scope、未知 client、`sub == client_id`、未绑定或不可用本地用户 |
| client-credentials/M2M token | 仅用于单独的机器授权接口（Issue #1 首版不接受） | 生态用户资源四个 GET 接口一律拒绝；通常表现为 `sub == client_id`，或显式 `gty=client_credentials` |

最后，客户端声明的事实和 token 类型的事实必须分开记录：`client_id` 说明“哪个 OAuth client 请求了 token”；`sub` 说明“token 所代表的主体”。只有在固定 Logto 版本和受信部署约定下，二者关系才可用于拒绝 M2M token。
