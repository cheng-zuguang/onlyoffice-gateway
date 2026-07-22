# 每服务独立 Webhook 凭证实施计划

> 状态：Gateway 自动化实现完成；doc 自动化证据已记录；真实保存联调待验收  
> 日期：2026-07-16  
> 涉及仓库：
>
> - Gateway：`/Users/cc/Documents/Projects/onlyoffice`
> - doc 后端：`/Users/cc/Documents/Projects/docx-workspace/docx-backend`

> 核验范围：2026-07-16 在 Gateway 仓库重新执行了 Go 全量测试、管理端测试与构建、secret 初始化脚本。doc 后端与前端的勾选项保留 2026-07-15 实施记录中的证据，本次未跨仓库重新执行；下方四项真实联调仍未完成。

## 1. 背景

改造前，Gateway 把全局 `JWT_SECRET` 同时用于以下安全职责：

1. Gateway 与 ONLYOFFICE Document Server 之间的编辑器配置 JWT。
2. 管理端 Session JWT。
3. Document Server 调用 Gateway `/callback` 时的 capability token。
4. Gateway 调用业务服务 webhook 时的 HMAC 签名。

这会迫使业务服务持有本应只属于 Gateway 和 Document Server 的全局密钥。任意一个业务服务泄露该值，都会扩大到管理端、callback 和其他业务服务。

当前实现已将上述职责拆分，并为每个 `service_id` 建立独立 webhook 凭证。业务服务只持有自己的 webhook secret。尚未勾选的工作仅是带真实 Document Server 和业务服务的端到端保存验收。

## 2. 目标

1. 每个业务服务拥有独立、随机生成的 webhook HMAC secret。
2. Gateway 不再使用 Document Server JWT secret 签名业务 webhook。
3. service secret 在 Gateway 中加密存储，管理 API 只在创建或轮换时返回一次明文。
4. webhook 协议支持时间窗口、服务身份和投递 ID，避免无期限重放。
5. doc 后端以无状态 verifier 接入，不新增数据库表。
6. 支持 current/previous 双密钥验证和十分钟回滚窗口。
7. 本地开发不兼容旧协议，配置错误时快速失败。

## 3. 非目标

1. 不修改 ONLYOFFICE Document Server 源码。
2. 不修改 `@zenmind-sdk/onlyoffice-editor` 包本身；doc 前端只移除重复 iframe 实现并调用已发布 SDK。
3. 不改造 `agent-platform`。
4. 不增加 webhook 投递数据库或消息队列。
5. 第一版不支持加密主密钥在线轮换。
6. 不保留旧 `JWT_SECRET` 的配置兼容或 webhook 验签回退。

## 4. 术语与安全边界

| 名称 | 持有方 | 用途 |
|---|---|---|
| `DOCUMENT_SERVER_JWT_SECRET` | Gateway、Document Server | ONLYOFFICE 编辑器配置 JWT |
| `GATEWAY_ADMIN_SESSION_SECRET` | 仅 Gateway | 管理端 Session JWT |
| `GATEWAY_CALLBACK_CAPABILITY_SECRET` | 仅 Gateway | Document Server callback capability |
| `WEBHOOK_SECRET_ENCRYPTION_KEY` | 仅 Gateway | AES-256-GCM 加密 service webhook secret |
| service webhook secret | Gateway、对应业务服务 | Gateway 业务 webhook HMAC |

所有 secret 必须由密码学安全随机源生成。普通签名 secret 至少 32 字节；`WEBHOOK_SECRET_ENCRYPTION_KEY` 必须为 Base64 编码的恰好 32 字节。

## 5. 公共接口

### 5.1 创建服务

`POST /admin/api/services`

请求保持现有字段：

```json
{
  "id": "doc",
  "public_key": "-----BEGIN PUBLIC KEY-----\n...",
  "allowed_webhook_domains": ["doc.codeshell.online"]
}
```

Gateway 自动生成 service webhook secret。响应只在本次请求返回明文：

```json
{
  "service": {
    "id": "doc",
    "public_key": "-----BEGIN PUBLIC KEY-----\n...",
    "allowed_webhook_domains": ["doc.codeshell.online"],
    "webhook_secret_configured": true,
    "webhook_secret_last_rotated_at": "2026-07-15T10:00:00Z"
  },
  "credentials": {
    "webhook_secret": "<base64url-secret>"
  }
}
```

响应必须包含：

```http
Cache-Control: no-store
```

`GET /admin/api/services`、`PUT /admin/api/services/{id}` 不得返回明文 secret、nonce 或 ciphertext。更新普通服务字段不得覆盖凭证状态。

### 5.2 轮换

`POST /admin/api/services/{id}/webhook-secret/rotate`

- 生成 pending secret。
- 只在本次响应返回 pending secret 明文。
- active secret 继续签名 webhook。
- 已存在 pending secret 时返回 `409`，避免未交付凭证被静默覆盖。

`POST /admin/api/services/{id}/webhook-secret/activate`

- pending 变为 active。
- 原 active 变为 previous。
- previous 设置十分钟过期时间。
- Gateway 从此只使用新 active secret 签名。

`POST /admin/api/services/{id}/webhook-secret/rollback`

- 只允许 previous 未过期时调用。
- previous 恢复为 active。
- 不再次返回 secret 明文。
- 超过窗口返回 `409`。

### 5.3 编辑会话创建

`POST /api/v1/documents`

- 请求携带非空 `webhook_url` 时，对应 service 必须存在 active webhook secret。
- 缺少 active secret 时返回：

```http
HTTP/1.1 422 Unprocessable Entity
Content-Type: application/json

{"error":"webhook credential not configured"}
```

- 请求不携带 `webhook_url` 时允许纯预览或临时编辑。
- 不得回退到全局 secret，也不得发送无签名 webhook。

## 6. Webhook v1 签名协议

### 6.1 请求头

```http
X-Gateway-Service-Id: doc
X-Gateway-Timestamp: 1784100000
X-Gateway-Delivery-Id: 019b0f64-...
X-Gateway-Signature: v1=<lowercase-hex-hmac-sha256>
```

### 6.2 签名原文

使用 UTF-8 和原始请求体字节，不重新序列化 JSON：

```text
v1\n<service_id>\n<timestamp>\n<delivery_id>\n<raw_body>
```

公式：

```text
signature = hex(HMAC-SHA256(service_secret, signing_input))
```

### 6.3 重试

- 同一业务事件的所有重试使用相同 `delivery_id`。
- 每次重试重新生成 `timestamp` 和签名。
- `2xx`：成功。
- 网络错误、`408`、`429`、`5xx`：指数退避重试。
- 其他 `4xx`：永久失败，不重试。

### 6.4 doc 验证

doc 后端在解析 JSON 前完成：

1. 检查 `service_id` 等于 `DOCX_ONLYOFFICE_SERVICE_ID`。
2. 检查时间戳格式，允许最大偏差默认 300 秒。
3. 检查 delivery ID 非空。
4. 使用 current secret 验证签名。
5. current 失败且 previous 存在时，再使用 previous 验证。
6. 使用恒定时间比较。
7. 所有认证失败统一返回 `401 invalid webhook signature`。
8. 业务回写失败返回 `500`，让 Gateway 重试。

`delivery_id` 仅用于日志与排障，不持久化。重复投递继续依赖现有文件 SHA1 检查实现内容幂等。

## 7. Gateway 内部设计

### 7.1 配置拆分

删除配置结构中的通用 `JWTSecret`，替换为：

```go
type Config struct {
    DocumentServerJWTSecret       string
    AdminSessionSecret            string
    CallbackCapabilitySecret      string
    WebhookSecretEncryptionKey    string
}
```

`WebhookSecretEncryptionKey` 在配置结构中保留 Base64 字符串，启动组合根通过 `WebhookSecretEncryptionKeyBytes()` 解码为 32 字节后交给持久化 service registry。

启动时严格校验：

- 四项全部存在。
- 签名 secret 至少 32 字节。
- 加密主密钥 Base64 解码后恰好 32 字节。
- 四项不得使用相同值。
- 旧 `JWT_SECRET` 不触发任何回退。

### 7.2 深模块边界

实际模块边界如下：

```go
// internal/handler：上传校验和 callback 投递只依赖 active 查询。
type WebhookCredentialResolver interface {
    ActiveWebhookSecret(serviceID string) (string, bool)
}
```

`internal/admin.InMemoryServiceStore` 是 service registry 的当前实现，同时提供身份解析和 `ActiveWebhookSecret`。创建、加密、rotate、activate、rollback 都由该模块自己的管理 API handler 调用；上传与 callback handler 只看到上面的窄接口，不接触加解密、pending 或 previous。

业务 JWT 继续通过 `internal/jwt.ServiceResolver` 的只读身份接口解析 RSA 公钥和 webhook 域名：

```go
type ServiceResolver interface {
    Resolve(id string) (*rsa.PublicKey, []string, bool)
}
```

`cmd/gateway` 是组合根：用加密主密钥打开同一 registry，再把它分别传给 Admin API、RS256 验签和 document runtime。callback 投递只依赖 active secret 查询接口。

### 7.3 持久化模型

```go
type EncryptedWebhookSecret struct {
    Version    int       `json:"version"`
    Nonce      string    `json:"nonce"`
    Ciphertext string    `json:"ciphertext"`
    CreatedAt  time.Time `json:"created_at"`
    ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

type WebhookCredentialState struct {
    Active   *EncryptedWebhookSecret `json:"active,omitempty"`
    Pending  *EncryptedWebhookSecret `json:"pending,omitempty"`
    Previous *EncryptedWebhookSecret `json:"previous,omitempty"`
}
```

AES-GCM Additional Authenticated Data 使用：

```text
onlyoffice-gateway:webhook-secret:v1:<service_id>:<slot>
```

这样密文不能被复制到另一个 service 或凭证槽位后继续解密。

`services.json` 使用 `0600` 权限。API DTO 与持久化结构必须分离，避免未来误序列化 secret 状态。

### 7.4 管理端

- 创建或轮换响应弹出一次性 secret 对话框。
- 提供复制按钮。
- 关闭后从 React state 清除。
- 不写入 localStorage、sessionStorage、URL 或日志。
- 服务列表只显示已配置状态、最后轮换时间、是否存在 pending。
- 激活和回滚要求二次确认。

### 7.5 审计

记录以下动作：

- service credential created
- rotation pending generated
- pending activated
- previous rolled back

审计字段只包含 service ID、动作、时间和结果，禁止包含 secret、nonce、ciphertext 或完整请求体。

## 8. doc 后端设计

### 8.1 配置

```yaml
docx:
  onlyoffice:
    service-id: ${DOCX_ONLYOFFICE_SERVICE_ID:}
    webhook-hmac-secret: ${DOCX_ONLYOFFICE_WEBHOOK_HMAC_SECRET:}
    webhook-hmac-previous-secret: ${DOCX_ONLYOFFICE_WEBHOOK_HMAC_PREVIOUS_SECRET:}
    webhook-max-clock-skew-seconds: ${DOCX_ONLYOFFICE_WEBHOOK_MAX_CLOCK_SKEW_SECONDS:300}
```

### 8.2 Verifier 公共接口

```java
public void verify(OnlyOfficeWebhookHeaders headers, byte[] rawBody);
```

该接口隐藏 canonical input、时间、current/previous secret 和恒定时间比较。时间作为依赖注入，测试不依赖系统时钟。

Controller 负责：

1. 读取原始 body。
2. 构造 headers。
3. 调用 verifier。
4. 解析 payload。
5. 调用现有文件回写。

## 9. TDD 纵向实施顺序

每个切片严格执行一个行为测试的 RED → 最小 GREEN → 必要重构。

### Slice 1：创建 service 时只返回一次独立 secret

- [x] RED：通过 Admin API 创建 service，响应包含一次性 secret；列表响应不包含 secret。
- [x] GREEN：最小随机 secret 生成与 API DTO。
- [x] REFACTOR：Admin API 使用独立 service view，列表不序列化内部凭证。

### Slice 2：service secret 加密落盘

- [x] RED：创建并重启 store 后仍可取得 active secret，持久化文件不包含明文。
- [x] GREEN：AES-256-GCM envelope。
- [x] RED：主密钥错误时加载失败。
- [x] GREEN：AES-GCM 认证失败会阻止 store 启动。

### Slice 3：带 webhook 的上传要求 active secret

- [x] RED：没有 secret 的 service 上传 webhook 会话返回 422；无 webhook 会话成功。
- [x] GREEN：上传边界查询 credential store。

### Slice 4：Gateway 使用 v1 协议投递

- [x] RED：真实 webhook HTTP 端点收到四个头，签名与固定向量一致。
- [x] GREEN：独立 webhook signer。
- [x] RED：401 不重试，429/500 重试且 delivery ID 稳定。
- [x] GREEN：响应分类和重签名。

### Slice 5：轮换、激活、回滚

- [x] RED：rotate 返回一次 pending secret，active 仍签名。
- [x] GREEN：pending 状态。
- [x] RED：activate 后使用新 secret，previous 十分钟内可回滚。
- [x] GREEN：状态转换和过期。

### Slice 6：Gateway 内部全局 secret 拆分

- [x] RED：缺少任一新配置时启动配置校验失败，旧 `JWT_SECRET` 无效。
- [x] GREEN：配置结构和调用点拆分。
- [x] 回归：复用 Gateway secret 被拒绝。
- [x] GREEN：独立性校验。

### Slice 7：doc 无状态 verifier

- [x] RED：固定 Gateway 测试向量可通过 doc verifier。
- [x] GREEN：current secret 验签。
- [x] RED：previous secret 可通过；错误 service、过期时间、篡改 body 返回认证失败。
- [x] GREEN：完整验证规则。

### Slice 8：doc webhook Controller

- [x] RED：合法请求触发现有回写；认证失败返回统一 401；业务失败返回 500。
- [x] GREEN：Controller 接入 verifier。
- [x] REFACTOR：移除旧 URL+body 签名逻辑。

### Slice 9：管理 UI 与配置工具

- [x] RED：组件测试确认 secret 只在创建/轮换响应后显示，关闭后清除。
- [x] GREEN：一次性对话框、pending 状态、激活和回滚确认。
- [x] RED：`make init-secrets` 不覆盖已有值。
- [x] GREEN：初始化脚本和手动命令文档。

### Slice 10：跨仓库联调

- [x] Gateway 全量 Go 测试通过。
- [x] Gateway 管理端测试与构建通过。
- [x] doc 聚焦测试通过。
- [x] doc 后端全量测试与编译通过。
- [x] doc 前端 ONLYOFFICE SDK 组件测试和 Vite 生产构建通过。
- [ ] 创建 service，复制 secret 到 doc 配置。
- [ ] 上传文档获得 `onlyOfficeEditUrl`。
- [ ] 保存文档后 webhook 验签成功并写回本地文件。
- [ ] 日志和响应中未出现任何 secret。

## 10. 本地初始化

### 10.1 自动生成

```bash
make init-secrets
```

要求：

- 从 `.env.example` 创建缺失的 `.env`。
- 只填充空值，不覆盖已有配置。
- 文件权限为 `0600`。
- 不把 secret 输出到终端。

### 10.2 手动生成

```bash
openssl rand -base64 32
```

分别执行四次并填入四个配置。不得复用同一个输出。

### 10.3 首次本地联调步骤

1. 初始化 Gateway：

   ```bash
   cd /Users/cc/Documents/Projects/onlyoffice
   cp .env.example .env
   make init-secrets
   # 编辑 .env，设置 ADMIN_PASSWORD；不要修改成相同的四个 secret
   docker compose up -d
   ```

2. 为 doc 生成 RS256 密钥对；私钥只给 doc，公钥注册到 Gateway：

   ```bash
   cd /Users/cc/Documents/Projects/docx-workspace/docx-backend
   openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out onlyoffice-private.pem
   openssl pkey -in onlyoffice-private.pem -pubout -out onlyoffice-public.pem
   chmod 600 onlyoffice-private.pem
   ```

3. 打开 Gateway 管理端，新增 service `doc`，填入 `onlyoffice-public.pem` 内容和 doc webhook 域名。立即复制一次性展示的 `webhook_secret`。

4. 创建 doc 本地配置：

   ```bash
   cd /Users/cc/Documents/Projects/docx-workspace/docx-backend
   cp .env.local.example .env.local
   ```

   必填项示例（所有 URL 都走配置，不在代码中写死）：

   ```dotenv
   DOCX_WORKSPACE=/Users/cc/Documents/Projects/docx-workspace/run/workspace
   DOCX_ONLYOFFICE_ENABLED=true
   DOCX_ONLYOFFICE_GATEWAY_URL=https://<gateway-domain>
   DOCX_ONLYOFFICE_PUBLIC_BASE_URL=https://<doc-tunnel-domain>
   DOCX_ONLYOFFICE_SERVICE_ID=doc
   DOCX_ONLYOFFICE_PRIVATE_KEY_FILE=./onlyoffice-private.pem
   DOCX_ONLYOFFICE_WEBHOOK_URL=https://<doc-tunnel-domain>/api/onlyoffice/webhook
   DOCX_ONLYOFFICE_WEBHOOK_HMAC_SECRET=<创建 service 时一次性返回的 webhook_secret>
   DOCX_ONLYOFFICE_WEBHOOK_HMAC_PREVIOUS_SECRET=
   DOCX_ONLYOFFICE_WEBHOOK_MAX_CLOCK_SKEW_SECONDS=300
   ```

5. 确认 tunnel 把公网域名转发到 doc 后端 `8080`，且该公网域名已加入 service webhook 白名单。启动 doc：

   ```bash
   ./deploy_local.sh
   ```

6. 打开文档后确认接口返回 `onlyOfficeEditUrl`；保存后检查 doc 日志包含同一个 `deliveryId`，文件被写回，Gateway webhook 成功指标增长。

### 10.4 凭证轮换顺序

1. 管理端点击“生成待切换凭证”，立即复制 pending secret；此时 Gateway 仍用旧 active secret 签名。
2. doc 配置把 pending secret 放入 `DOCX_ONLYOFFICE_WEBHOOK_HMAC_SECRET`，把旧 active secret 放入 `DOCX_ONLYOFFICE_WEBHOOK_HMAC_PREVIOUS_SECRET`，重启 doc。
3. 管理端点击“激活待切换凭证”。Gateway 开始使用新 secret；doc current 验证成功。
4. 做一次真实保存验证。若失败，在十分钟内点击“回滚 Webhook 凭证”；doc previous 仍能验证旧 secret。
5. 稳定后清空 doc 的 previous 配置并重启。Gateway previous 到期后不再允许回滚。

## 11. 验收标准

1. doc 配置中不再出现 Document Server JWT secret。
2. 两个 service 获得不同 webhook secret。
3. 泄露某个 service secret 不能伪造其他 service webhook。
4. `services.json`、审计日志、应用日志、Admin GET API 均不出现明文 secret。
5. 修改 webhook body、service ID、timestamp 或 delivery ID 后验签失败。
6. 纯预览不强制要求 webhook secret。
7. 轮换错误可在十分钟内回滚。
8. 所有行为由公共 HTTP API或 verifier 公共接口测试覆盖。

## 12. 实施记录

| 日期 | Slice | RED 证据 | GREEN 证据 | 备注 |
|---|---|---|---|---|
| 2026-07-15 | 规划 | — | — | 公共接口与优先行为已确认 |
| 2026-07-15 | 1 | 创建 API 缺少 `Cache-Control: no-store` | `go test ./internal/admin -run TestCreateServiceReturnsWebhookSecretOnce` | 创建响应一次性返回凭证，列表隐藏 |
| 2026-07-15 | 2a | 加密 store 构造器和 active secret 查询不存在 | `go test ./internal/admin -run TestWebhookSecretPersistsEncryptedAcrossRestarts` | active secret 使用 AES-256-GCM 持久化 |
| 2026-07-15 | 2b | 错误主密钥必须阻止加载 | `go test ./internal/admin -run TestEncryptedWebhookSecretRejectsWrongMasterKey` | AES-GCM 认证失败即启动失败 |
| 2026-07-15 | 3 | 缺少 service secret 的 webhook 会话仍返回 201 | `go test ./internal/gateway -run TestUploadRequiresCredentialOnlyWhenWebhookRequested` | 无 webhook 的预览/临时会话仍允许 |
| 2026-07-15 | 4 | webhook 缺少 v1 四头；401 被重试 | `go test ./internal/gateway -run 'TestWebhookIncludesVersionedServiceSignature|TestWebhookDoesNotRetryPermanentClientError'` | delivery ID 跨重试稳定 |
| 2026-07-15 | 5 | rotate/activate/rollback 路由返回 404 | `go test ./internal/admin` | pending/active/previous 状态与十分钟窗口完成 |
| 2026-07-15 | 6 | 旧 `JWT_SECRET` 仍可满足配置 | `go test ./internal/config ./cmd/gateway` | 四个内部 secret 完全拆分且禁止复用 |
| 2026-07-15 | 7 | doc verifier 不存在 | `mvn -Dtest=OnlyOfficeWebhookVerifierTest test` | 固定向量、previous、时间与篡改验证完成 |
| 2026-07-15 | 8 | Controller 没有 verifier；保存异常返回 200 | `mvn -Dtest=OnlyOfficeWebhookControllerAuthenticationTest test` | 认证失败 401，处理失败 500 |
| 2026-07-15 | 9 | UI 不展示一次性凭证；初始化脚本不存在 | `npm test`、`npm run build`、`make test-init-secrets` | 创建/轮换/激活/回滚 UI 完成 |
| 2026-07-15 | 10a | — | `go test ./...`、`mvn test` | 自动化跨模块回归通过，待真实浏览器保存联调 |
| 2026-07-15 | 10b | 前端已安装 SDK 但仍自写 iframe | `pnpm test:onlyoffice`、`pnpm build` | Office 预览改为调用 `@zenmind-sdk/onlyoffice-editor` |
