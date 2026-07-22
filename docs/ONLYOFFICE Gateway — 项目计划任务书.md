# ONLYOFFICE Gateway — 项目计划任务书

## 文档版本

| 版本 | 日期 | 说明 |
|---|---|---|
| v1.0 | 2026-07-02 | 初始版本，含全部架构决策与实施计划 |


## 1. 项目背景

### 1.1 问题陈述

多套线上业务服务需要集成 ONLYOFFICE 文档编辑能力（Word/PPT/Excel），但标准的 ONLYOFFICE Docs API 要求：

1. **业务后端暴露文件下载接口** → `document.url` 必须能被 Document Server 访问
2. **业务后端暴露回调接口** → `editorConfig.callbackUrl` 必须能接收 POST
3. **业务后端实现 JWT 签名** → 生成 ONLYOFFICE config 需要签 token
4. **业务前端加载 api.js + 初始化编辑器** → 每个前端都要嵌入编辑器逻辑

如果每个业务服务都各自适配，改造量大、侵入性强，且多个服务各维护一套集成代码，长期维护成本高。

### 1.2 解决方案

构建一个**独立的 ONLYOFFICE Gateway 服务**，将所有 ONLYOFFICE 特有的协议细节收敛到 Gateway 内部，业务服务只需通过极简 API 接入：

```
业务服务 A/B/C            Gateway                    ONLYOFFICE Document Server
    │                       │                              │
    │── 上传文件 ──────────→│                              │
    │   (JWT 自签)          │                              │
    │                       │── 生成 config + JWT ────────→│
    │                       │   document.url = Gateway URL │
    │                       │   callbackUrl = Gateway URL  │
    │                       │                              │
    │                       │←── GET download ─────────────│
    │                       │── 返回文件 ─────────────────→│
    │                       │                              │
    │                       │   用户编辑文档               │
    │                       │                              │
    │                       │←── POST callback ────────────│
    │                       │   status=2, url=...          │
    │                       │── 下载新文件，存本地 ────────│
    │                       │                              │
    │←── Webhook 通知 ─────│                              │
    │── GET 拉取文件 ──────→│                              │
    │←── 返回结果 ─────────│                              │
```

**业务服务零入侵**：只需要调用 HTTP 上传文件、收 Webhook、拉回结果，完全不感知 ONLYOFFICE 协议。

---

## 2. 架构决策汇总

以下 12 项关键决策通过系统化评估确定：

| # | 决策项 | 选择 | 理由 |
|---|---|---|---|
| 1 | Gateway 部署形态 | **独立 Gateway 服务** | 一个实例服务所有业务，无需改 Document Server |
| 2 | 文档交换模型 | **按文档选择 hosted multipart 或 direct `source_url`** | 本地文件可推送托管，已有 HTTPS 文件可保持业务侧存储所有权 |
| 3 | 通知机制 | **业务 Webhook**；hosted 模式可主动拉取结果 | 当前没有 SSE 接口；direct-source 结果必须在 webhook 中及时保存 |
| 4 | 文档接入方式 | **同一 `POST /api/v1/documents` 的两种 ingress mode** | 不为不同业务形态拆分 Gateway 架构 |
| 5 | 文档存储 | **hosted 支持本地磁盘或 S3；direct-source 仅存元数据** | Gateway 只管理自己托管的临时对象 |
| 6 | 文档 TTL | **8 小时** | 超时自动清理，清理前服务可多次拉取 |
| 7 | 服务认证 | **JWT 自签 + 公钥验签 + 域名白名单** | 服务自治，Gateway 不引入用户体系 |
| 8 | 编辑器呈现 | **Gateway 独立编辑器页 → iframe 内嵌** | 服务前端只需一行 iframe |
| 9 | 编辑器定制 | **有限定制 + 分层 merge → 预留完全 override** | 品牌适配能力强，扩展时零改动 |
| 10 | Gateway 权限边界 | **服务级别**（不触及用户体系） | 最小认知模型 |
| 11 | Webhook 重试 | **默认最多重试 3 次，按响应分类** | 网络错误、408、429、5xx 重试；其他 4xx 永久失败 |
| 12 | 前端 SDK | **极薄 npm 包**（`<OnlyOfficeEditor>`） | 单组件封装 iframe + postMessage |

---

## 3. 系统架构

### 3.1 组件全景

```
┌───────────────────────────────────────────────────────────────────┐
│                         业务服务 A / B / C                         │
│                                                                   │
│  上游: POST /documents (multipart + JWT)                           │
│  下游: Webhook 接收通知 → GET /documents/{id} 拉取结果               │
│  前端: <OnlyOfficeEditor token={jwt} onSaveDone={fn} />            │
│                                                                   │
└───────────────────────────┬───────────────────────────────────────┘
                            │ HTTP (HTTPS 优先)
                            ▼
┌───────────────────────────────────────────────────────────────────┐
│                      ONLYOFFICE Gateway                            │
│                                                                   │
│   HTTP Handlers:                                                   │
│   ┌─────────────────────────────────────────────────────────┐     │
│   │ POST   /api/v1/documents          → 上传文档 + JWT 验签  │     │
│   │ GET    /api/v1/documents/{id}     → 下载编辑结果         │     │
│   │ DELETE /api/v1/documents/{id}     → 手动清理 (可选)      │     │
│   │ GET    /api/v1/health             → 健康检查             │     │
│   │ GET    /edit?token={jwt}          → 编辑器 HTML 页面     │     │
│   │ GET    /download/{docId}          → ONLYOFFICE 下载文件   │     │
│   │ POST   /callback                  → ONLYOFFICE 回调      │     │
│   └─────────────────────────────────────────────────────────┘     │
│                                                                   │
│   Core Modules:                                                    │
│   ┌─────────────────────────────────────────────────────────┐     │
│   │ config      → 配置加载 (YAML) + 公钥解析                 │     │
│   │ jwt         → JWT 验签 + ONLYOFFICE config 签名           │     │
│   │ storage     → 文档存储 (local → 预留 S3)                  │     │
│   │ configbuilder → ONLYOFFICE config 分层 merge              │     │
│   │ webhook     → Webhook 发送 + 指数退避重试                 │     │
│   │ handler     → HTTP 路由 + 业务逻辑                        │     │
│   └─────────────────────────────────────────────────────────┘     │
│                                                                   │
│   Storage: 本地磁盘 (data/storage/)                                │
│   Cleaner: 定时 TTL 扫描清理过期文档                                │
│                                                                   │
└───────────────────────────┬───────────────────────────────────────┘
                            │ HTTP
                            ▼
┌───────────────────────────────────────────────────────────────────┐
│                  ONLYOFFICE Document Server                        │
│                                                                   │
│   地址:  https://doc-server (Gateway 配置中指定)                    │
│                                                                   │
│   ← GET  /download/{docId}    → Gateway 返回本地文件               │
│   → POST /callback            → Gateway 处理保存                   │
│                                                                   │
└───────────────────────────────────────────────────────────────────┘
```

### 3.2 数据流

#### 3.2.1 文档上传

Gateway 不按桌面端/线上服务拆两套架构。业务服务统一调用 `POST /api/v1/documents` 创建编辑会话，并按文档选择接入模式：

- **multipart 上传托管**：适用于桌面端、本地工作区、没有公网 HTTPS 文件地址的服务。业务服务主动上传文件字节，Gateway 临时保存原文件和编辑结果。
- **`source_url` 直连**：适用于线上服务已能提供 Document Server 可访问的短时效 HTTPS GET URL。Gateway 只保存会话元数据，不复制业务文件。

```
业务服务 → POST /api/v1/documents
  Headers:  Authorization: Bearer <JWT>
  Body A:   multipart/form-data { file: <binary>, meta: <JSON> }
  Body B:   empty body + JWT source_url claim

JWT payload:
  {
    "service_id":  "crm-service",
    "external_id": "contract-2024-001",
    "webhook_url": "https://crm.mycompany.com/internal/onlyoffice-callback",
    "user":         { "id": "u-123", "name": "张三" },
    "file_name":    "合同模板.docx",
    "document_type": "word",          // word | cell | slide | pdf
    "source_url":    "https://biz.example.com/files/contract.docx?token=...", // 直连模式可选
    "branding": {                     // 有限定制 (可选)
      "logo_url":    "https://crm.mycompany.com/logo.png",
      "color_theme": "#1a73e8",
      "language":    "zh-CN"
    },
    "config_overrides": { ... }       // 完全定制 (预留, 可选)
  }

Gateway 响应:
  201 Created
  {
    "document_id": "doc_abc123",
    "status":      "uploaded",
    "expires_at":  "2026-07-02T22:00:00Z"
  }
```

多个业务服务可以共用同一个 Gateway，但各自管理自己的文档存储和保存逻辑。Gateway 在 multipart 模式中只是临时编辑存储，在 `source_url` 模式中只是协议编排和会话元数据存储。

#### 3.2.2 编辑器打开

```
业务前端 → iframe src="https://gateway/edit?token=<editor_jwt>"

Gateway → 返回 HTML 页面:
  <!DOCTYPE html>
  <html>
  <head>
    <script src="https://doc-server/web-apps/apps/api/documents/api.js"></script>
  </head>
  <body>
    <div id="placeholder"></div>
    <script>
      const config = /* Gateway 构建的 ONLYOFFICE config */;
      const docEditor = new DocsAPI.DocEditor("placeholder", config);

      // postMessage: 编辑器就绪
      window.parent.postMessage({ type: "onlyoffice:ready" }, "*");

      // postMessage: 保存完成
      docEditor.addEventListener("onDocumentStateChange", (e) => {
        window.parent.postMessage({ type: "onlyoffice:saved", data: e.data }, "*");
      });
    </script>
  </body>
  </html>
```

#### 3.2.3 编辑完成 & 通知

```
ONLYOFFICE Document Server → POST /callback (Gateway)
  Body: { "status": 2, "key": "doc_abc123", "url": "https://doc-server/cache/xxx.docx" }

Gateway 内部:
  1. 从 data.url 下载编辑后的文件
  2. 存入 storage，替换原文件
  3. 标记 document 状态为 edited
  4. 触发 Webhook → 业务服务

Gateway → POST <service webhook_url>
  Body: {
    "event": "document.saved",
    "document_id": "doc_abc123",
    "external_id": "contract-2024-001",
    "status": "ready"
  }

业务服务 → GET /api/v1/documents/doc_abc123
Gateway → 返回编辑后的文件二进制
```

#### 3.2.4 Webhook 重试

默认 `WEBHOOK_MAX_RETRIES=3`，含首次投递最多尝试 4 次。三次重试分别在约 1s、2s、4s 的指数退避后执行，并各附加最多 250ms jitter。网络错误、408、429、5xx 才会进入重试；其他 4xx 立即视为永久失败。

hosted multipart 的编辑结果在投递 webhook 前已经保存到 Gateway，业务服务可稍后主动 `GET /api/v1/documents/{id}`。direct-source 模式不在 Gateway 保存结果字节，业务服务必须通过成功处理 webhook 中的短时效 `edited_url` 完成持久化，不能依赖 Gateway 轮询兜底。

---

## 4. API 详细设计

### 4.1 对外 API（业务服务调用）

#### `POST /api/v1/documents`

上传文档到 Gateway。

| 项目 | 值 |
|---|---|
| **认证** | `Authorization: Bearer <JWT>`（服务自签，Gateway 用公钥验签） |
| **Content-Type** | `multipart/form-data` |
| **字段** | `file`（二进制）、`meta`（JSON 字符串，可选，覆盖 JWT 中的元数据） |

**JWT Claims（上传用）**:

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `service_id` | string | ✅ | 服务标识，对应 Gateway 配置中的 service |
| `webhook_url` | string | | 编辑完成后的回调地址；提供时域名必须在白名单内，且 service 必须有 active webhook credential；省略时不投递业务 webhook |
| `external_id` | string | | 业务侧的文档标识 |
| `user.id` | string | | 编辑者 ID |
| `user.name` | string | | 编辑者名称 |
| `file_name` | string | | 文件名（默认取上传文件名） |
| `document_type` | string | | `word` / `cell` / `slide` / `pdf`，默认从 file_name 后缀推断 |
| `branding` | object | | 有限定制（见 4.3 节） |
| `config_overrides` | object | | 完全定制（预留），见 4.3 节 |
| `exp` | number | ✅ | 上传 token 有效期，控制调用 API 的时间窗口。生成后应立即使用，建议 60s 以内以防范重放攻击 |

**响应**:

```json
{
  "document_id": "doc_abc123def456",
  "status": "uploaded",
  "expires_at": "2026-07-02T22:00:00Z"
}
```

#### `GET /api/v1/documents/{id}`

下载编辑完成的文档。

| 项目 | 值 |
|---|---|
| **认证** | `Authorization: Bearer <JWT>` |
| **返回** | 文件二进制 + `Content-Disposition: attachment` |

**状态码**:

| 状态 | 语义 |
|---|---|
| `200` | 下载成功 |
| `404` | 文档不存在或已过期 |
| `409` | 文档尚未编辑完成 |
| `425` | 文档已过期被清理 |

#### `DELETE /api/v1/documents/{id}`

手动清理文档（可选，非必需——TTL 自动兜底）。

#### `GET /api/v1/health`

健康检查，返回 `{"status": "ok"}`。

### 4.2 内部 API（ONLYOFFICE Document Server 专用）

#### `GET /download/{docId}`

ONLYOFFICE Document Server 下载原文件。此路由不在公开 API 中，仅供 Document Server 调用。

- Gateway 从 storage 读取上传时的原始文件返回；业务下载接口仍读取最新版（优先 edited）
- 响应包含 `Content-Length`、`ETag`、`Last-Modified`、`Accept-Ranges`
- 使用 `Cache-Control: private, max-age=28800`，便于 Document Server 在同一编辑窗口内复用原文件
- 使用标准文件响应，支持 Range 与条件请求
- 仅允许来自 `document_server_url` 的请求（TODO: IP filter）

#### `POST /callback`

ONLYOFFICE Document Server 回调，携带编辑结果。

**请求体**（ONLYOFFICE 标准回调格式）:

```json
{
  "status": 2,
  "key": "doc_abc123def456",
  "url": "https://doc-server/cache/temp_xxx.docx",
  "filetype": "docx",
  "actions": [...],
  "users": ["u-123"]
}
```

Gateway 处理逻辑：
1. `status === 2` 或 `status === 6` → debounce 合并 → 从 `url` 下载编辑后文件 → 保存为最新版 → 触发 webhook
2. `status === 1` → 用户连接/断开通知
3. `status === 4` → 文档关闭无变更
4. `status === 3` 或 `7` → 保存错误

回调下载和 Webhook 投递使用带连接池的 HTTP client，配置 keep-alive、空闲连接池、TLS 握手超时与响应头超时，减少高并发保存时的连接建立成本。

### 4.3 编辑器配置分层 merge

Gateway 生成的最终 ONLYOFFICE config 由三层 merge 构成（优先级从低到高）：

```
Layer 1: Gateway 默认值
  document.key, document.url, document.fileType, document.title,
  editorConfig.callbackUrl, editorConfig.user

Layer 2: JWT branding 字段（有限定制）
  editorConfig.customization.logo → branding.logo_url
  editorConfig.customization.colors → branding.color_theme
  editorConfig.lang → branding.language
  document.permissions → 从 branding 推断

Layer 3: JWT config_overrides（完全定制 - 预留）
  调用方穿透覆盖任意 ONLYOFFICE config 字段
```

**branding 字段映射**:

| JWT `branding` 字段 | ONLYOFFICE config 映射 |
|---|---|
| `logo_url` | `editorConfig.customization.logo.image` |
| `color_theme` | `editorConfig.customization.colors` |
| `language` | `editorConfig.lang` |

**config_overrides 示例**（未来完全定制）:

```json
{
  "config_overrides": {
    "customization": {
      "compactToolbar": true,
      "hideRightMenu": true,
      "autosave": true
    },
    "permissions": {
      "comment": false,
      "download": false
    }
  }
}
```

### 4.4 编辑器页面

`GET /edit?token=<editor_jwt>`

**editor_jwt Claims**:

| 字段 | 必填 | 说明 |
|---|---|---|
| `document_id` | ✅ | 已上传的文档 ID |
| `exp` | ✅ | 编辑器 token 有效期，控制从生成 token 到首次加载编辑器页面的时间窗口。token 仅在浏览器请求 /edit 页面时校验一次，页面加载后不再使用。较短的过期时间可防止编辑器 URL 泄露后被长期滥用 |

Gateway 返回一个完整的 HTML 页面，内嵌 ONLYOFFICE 编辑器。页面逻辑：

1. 从 `editor_jwt` 解析 `document_id`
2. 从 storage 获取文档 meta
3. 构建完整 ONLYOFFICE config（三层 merge）
4. 签名 config（JWT，仅使用 `DOCUMENT_SERVER_JWT_SECRET`）
5. 渲染 `<script src="doc-server/.../api.js">`
6. 初始化 `DocsAPI.DocEditor("placeholder", config)`
7. postMessage 到父窗口：
   - `{ type: "onlyoffice:ready" }` — 编辑器就绪
   - `{ type: "onlyoffice:saved" }` — 文档保存完成
   - `{ type: "onlyoffice:error", message }` — 错误

### 4.5 Webhook 通知格式

Gateway → 业务服务：

```json
POST <webhook_url>
Headers:
  Content-Type: application/json
  X-Gateway-Event: document.saved
  X-Gateway-Service-Id: <service_id>
  X-Gateway-Timestamp: <unix-seconds>
  X-Gateway-Delivery-Id: <stable-delivery-id>
  X-Gateway-Signature: v1=<lowercase-hex-hmac-sha256>

Body:
{
  "event": "document.saved",
  "document_id": "doc_abc123def456",
  "external_id": "contract-2024-001",
  "status": "ready",
  "edited_url": "https://document-server/..."
}
```

`edited_url` 只在 `source_url` 直连模式中传递；multipart 托管模式为空，业务服务通过 `GET /api/v1/documents/{id}` 拉取 Gateway 保存的最新版。

**Webhook v1 签名校验**（业务服务侧）:
```
signing_input = "v1\n" + service_id + "\n" + timestamp + "\n" + delivery_id + "\n" + raw_body
expected = hex(HMAC-SHA256(<该 service 的 webhook_secret>, signing_input))
actual   = X-Gateway-Signature 去掉 "v1=" 前缀后的值
constant_time_verify(expected, actual)
```

业务服务应在解析 JSON 前验证 service ID、时间戳偏差、delivery ID 和原始 body 签名。同一事件重试保持 delivery ID 不变，每次更新 timestamp 和签名。网络错误、408、429、5xx 重试；其他 4xx 不重试。

---

## 5. 存储设计

### 5.1 本地存储（当前实现）

```
{storage_dir}/
  doc_abc123/
    meta.json       → 元数据
    original.docx   → 原始文件
    edited.docx     → 编辑后文件（存在 = 已编辑）
```

读取语义：

- `GetOriginal`：供 `/download/{docId}` 使用，只返回上传原件，保证 Document Server 编辑会话稳定。
- `Get`：供业务下载使用，优先返回 `edited.docx`，没有编辑结果时回退 `original.docx`。
- 本地存储按 document ID 加锁，同一文档内读写串行保护，不同文档之间可并发处理，避免大文件保存阻塞其他文档读取。

**meta.json 结构**:

```json
{
  "document_id": "doc_abc123def456",
  "service_id": "crm-service",
  "external_id": "contract-2024-001",
  "webhook_url": "https://crm.mycompany.com/internal/onlyoffice-callback",
  "file_name": "合同模板.docx",
  "file_type": "docx",
  "document_type": "word",
  "created_at": "2026-07-02T14:00:00Z",
  "expires_at": "2026-07-02T22:00:00Z",
  "is_edited": true,
  "edited_at": "2026-07-02T14:30:00Z",
  "branding": {
    "logo_url": "https://crm.mycompany.com/logo.png",
    "color_theme": "#1a73e8",
    "language": "zh-CN"
  },
  "config_overrides": null
}
```

### 5.2 对象存储接口（预留）

```go
// Store 接口定义了所有存储操作。
type Store interface {
    Put(documentID string, reader io.Reader, meta Meta) error
    Get(documentID string) (io.ReadCloser, error)
    PutEdited(documentID string, reader io.Reader) error
    GetMeta(documentID string) (*Meta, error)
    MarkEdited(documentID string) error
    Delete(documentID string) error
    Expire() (int, error)
}
```

本地实现已就绪。未来换 S3/MinIO 只需替换实现，不改任何 handler 代码。

### 5.3 定时清理

Gateway 启动一个 goroutine，每 30 分钟扫描一次存储：

```
SELECT * FROM meta WHERE expires_at < NOW()
→ DELETE 所有过期文档的文件和 meta
```

---

## 6. 安全设计

| 层 | 机制 | 说明 |
|---|---|---|
| **传输** | HTTPS | 所有业务服务 → Gateway 通信走 TLS |
| **服务认证** | JWT (RS256) 自签 + Gateway 公钥验签 | 每个服务的 RSA 公钥预配置在 Gateway 中 |
| **域名白名单** | `allowed_webhook_domains` | Gateway 校验 webhook_url 域名，防止 JWT 泄露后任意回调 |
| **ONLYOFFICE 签名** | JWT (HS256) | Gateway 仅用与 Document Server 共享的 `DOCUMENT_SERVER_JWT_SECRET` 签 config |
| **Webhook 防伪** | 每服务独立凭证 + Webhook v1 HMAC-SHA256 | 服务可验证 webhook 来自 Gateway，且一个服务的凭证不能伪造其他服务 |
| **文件访问** | `/download/{docId}` 与业务下载路由由部署网络保护 | 当前 handler 不做调用方身份认证，生产环境必须限制暴露面 |
| **文档隔离** | 每个 document ID 独立存储键 | ID 随机不可预测，但当前下载 API 不提供 service 级授权边界 |
| **最小暴露** | Gateway 只监听内网地址或通过反向代理 | 不直接暴露到公网 |
| **TTL 清理** | 8 小时自动过期 | 防止磁盘耗尽 |

### 6.1 Gateway 配置示例

```yaml
# gateway.yaml
listen_addr: "127.0.0.1:18080"
document_server_url: "https://doc.zenmind.cc"
document_server_jwt_secret: "<独立随机值>"
gateway_admin_session_secret: "<独立随机值>"
gateway_callback_capability_secret: "<独立随机值>"
webhook_secret_encryption_key: "<Base64 编码的 32 字节值>"
storage_backend: "local"
storage_dir: "./data/storage"
ttl_hours: 8
webhook_max_retries: 3
```

Service 不从 YAML 加载。管理员通过 `/admin/api/services` 或管理 UI 写入持久化 service registry；创建响应会一次性返回该 service 的 webhook credential。

### 6.2 业务服务接入流程

```
1. 生成 RSA 密钥对（一次）
   openssl genpkey -algorithm RSA -out private.pem -pkeyopt rsa_keygen_bits:2048
   openssl rsa -pubout -in private.pem -out public.pem

2. Gateway 管理员通过管理 UI 注册 service、公钥和 webhook 域名白名单，并把创建响应一次性展示的 webhook secret 安全交付给该业务服务

3. 上传文档时用私钥签 JWT:
   const token = jwt.sign({
     service_id: "crm-service",
     webhook_url: "https://crm.mycompany.com/...",
     ...
   }, privateKey, { algorithm: "RS256", expiresIn: "60s" });

4. 调用 POST /api/v1/documents，携带 token
```

---

## 7. 实施计划

### 阶段一：基础设施（3 天）

| # | 任务 | 产出 | 依赖 |
|---|---|---|---|
| P1-1 | 项目骨架搭建 | `go.mod`, `cmd/gateway/main.go`, Makefile, Dockerfile | - |
| P1-2 | 配置模块 | `internal/config/config.go`，支持 YAML 加载 + 公钥解析 | - |
| P1-3 | 本地存储实现 | `internal/storage/local.go`，实现 Store 接口 | - |
| P1-4 | JWT 工具模块 | `internal/jwt/jwt.go`，服务 JWT 验签 + ONLYOFFICE config 签名 | P1-2 |
| P1-5 | TTL 清理器 | 定时 goroutine 扫描过期文档 | P1-3 |
| P1-6 | 健康检查端点 | `GET /api/v1/health` | - |

### 阶段二：核心 API（3 天）

| # | 任务 | 产出 | 依赖 |
|---|---|---|---|
| P2-1 | 上传 API | `POST /api/v1/documents`，multipart + JWT 验签 + 域名白名单校验 | P1-3, P1-4 |
| P2-2 | 下载 API | `GET /api/v1/documents/{id}`，返回编辑后文件 | P1-3 |
| P2-3 | ONLYOFFICE 下载路由 | `GET /download/{docId}`，供 Document Server 下载原文件 | P1-3 |
| P2-4 | ONLYOFFICE 回调处理 | `POST /callback`，解析 OO 回调，下载新文件 → 替换存储 | P1-3 |

### 阶段三：编辑器与 Webhook（3 天）

| # | 任务 | 产出 | 依赖 |
|---|---|---|---|
| P3-1 | Config Builder | `internal/configbuilder/builder.go`，三层 merge 生成 ONLYOFFICE config | P1-4 |
| P3-2 | 编辑器 HTML 页面 | `GET /edit?token=`，嵌入 ONLYOFFICE api.js + postMessage | P2-1, P3-1 |
| P3-3 | Webhook 发送器 | `internal/handler/callback.go`，多次重试 + HMAC 签名 | P2-4 |
| P3-4 | 编辑器 JWT 页面 | `GET /edit?token=` 验证 token → 返回编辑器页面 HTML | P3-2 |

### 阶段四：前端 SDK + 集成测试（2 天）

| # | 任务 | 产出 | 依赖 |
|---|---|---|---|
| P4-1 | npm 包 `@zenmind/onlyoffice-editor` | React 组件封装 iframe + postMessage + 轮询降级 | P3-2 |
| P4-2 | 端到端测试 | 上传 → 编辑 → 保存 → webhook → 拉取 全链路 | P2-1~P3-3 |
| P4-3 | Docker 化 | Dockerfile + docker-compose（Gateway + 可选 OO Document Server） | P2-1~P3-3 |

**总计：11 天**

---

## 8. 项目结构

```
onlyoffice-gateway/
├── cmd/gateway/
│   └── main.go                 # 入口：加载配置 → 启动 HTTP server
├── internal/
│   ├── admin/
│   │   └── handler.go          # Admin 登录 + Service CRUD + services.json 持久化
│   ├── config/
│   │   └── config.go           # 配置加载（环境变量 + 可选 YAML）
│   ├── jwt/
│   │   └── jwt.go              # RS256 验签 + HS256 签名
│   ├── storage/
│   │   ├── interface.go        # Store 接口（预留 S3）
│   │   └── local.go            # 本地磁盘实现
│   ├── configbuilder/
│   │   └── builder.go          # ONLYOFFICE config 分层 merge
│   ├── gateway/
│   │   ├── server.go           # HTTP router + ServiceResolver 边界
│   │   └── middleware.go       # 统一访问日志
│   └── handler/
│       ├── upload.go           # POST /api/v1/documents
│       ├── download.go         # GET /api/v1/documents/{id}
│       ├── callback.go         # POST /callback (ONLYOFFICE)
│       ├── editor.go           # GET /edit?token= (编辑器页面)
│       └── helpers.go          # JSON 响应等 handler helper
├── admin-ui/
│   └── src/                    # 管理端 SPA
├── frontend-sdk/
│   ├── package.json
│   └── src/
│       ├── OnlyOfficeEditor.tsx # React 组件
│       └── index.ts            # SDK 导出
├── .env.example                # 环境变量模板
├── Makefile
├── Dockerfile
├── docker-compose.yml
├── go.mod
└── go.sum
```

---

## 9. 前端 SDK 接口

```tsx
// @zenmind/onlyoffice-editor

import { OnlyOfficeEditor } from "@zenmind/onlyoffice-editor";

function MyPage() {
  return (
    <OnlyOfficeEditor
      token={editorJwt}                              // Gateway 编辑器 JWT
      gatewayUrl="https://gateway.mycompany.com"     // Gateway 地址 (默认从 token 推断)
      onReady={() => console.log("编辑器就绪")}
      onSaved={(event) => {
        console.log("保存完成:", event.document_id);
        // 此时可从 GET /api/v1/documents/{id} 拉取结果
      }}
      onError={(err) => console.error("编辑器错误:", err)}
      style={{ width: "100%", height: "600px" }}
    />
  );
}
```

**组件内部**:

1. 构造 iframe `src={gatewayUrl}/edit?token={token}`
2. 监听 `window.message` 事件，解析 `onlyoffice:ready` / `onlyoffice:saved` / `onlyoffice:error`
3. `onSaved` 未在 N 秒内触发时，内部启动轮询 `/api/v1/documents/{document_id}` 降级检查

---

## 10. Docker 部署

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o gateway ./cmd/gateway

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/gateway .
VOLUME /app/data
EXPOSE 18080
ENTRYPOINT ["./gateway"]
```

```yaml
services:
  document-server:
    image: onlyoffice/documentserver:latest

  gateway:
    build: .
    ports:
      - "18080:18080"
    environment:
      DOCUMENT_SERVER_URL: http://document-server
      DOCUMENT_SERVER_JWT_SECRET: ${DOCUMENT_SERVER_JWT_SECRET}
      GATEWAY_ADMIN_SESSION_SECRET: ${GATEWAY_ADMIN_SESSION_SECRET}
      GATEWAY_CALLBACK_CAPABILITY_SECRET: ${GATEWAY_CALLBACK_CAPABILITY_SECRET}
      WEBHOOK_SECRET_ENCRYPTION_KEY: ${WEBHOOK_SECRET_ENCRYPTION_KEY}
      ADMIN_USERNAME: ${ADMIN_USERNAME:-admin}
      ADMIN_PASSWORD: ${ADMIN_PASSWORD}
      SERVICE_STORE_PATH: /app/data/services.json
    volumes:
      - gateway-data:/app/data
    restart: unless-stopped

volumes:
  gateway-data:
```

---

## 11. 部署风险与缓解

| 风险 | 概率 | 缓解 |
|---|---|---|
| Gateway 单点故障 | 中 | 当前 service registry 与审计日志为实例本地状态；先做持久卷和备份，高可用需另行设计共享控制面，不能直接按无状态服务扩容 |
| 大文件上传 OOM | 低 | multipart stream → 直接写盘，不缓冲到内存 |
| Webhook 不可达导致通知丢失 | 低 | 分类重试与失败指标；hosted 模式可主动拉取，direct-source 模式必须保证接收端高可用并及时保存 `edited_url` |
| 存储磁盘占满 | 中 | TTL 8h 自动清理 + 磁盘水位告警 |
| 密钥泄露 | 低 | RS256 服务密钥与四个 Gateway 信任域分离；业务服务只持有自己的 webhook credential，按 pending/activate/rollback 流程轮换 |

---

## 12. 协同编辑风险与缓解

### 12.1 整体判断

协同编辑由 Document Server 内部的 Operational Transformation 引擎负责，Gateway 只负责文件存取。架构本身不破坏协同，但需要在以下 6 个方面做对。

---

### 12.2 风险 1：Callback 风暴 — 🔴 高影响，好修复

**现象**：多人协同编辑时，Document Server 频繁发出 callback（每人连接/断开都发 status=1）。如果 Gateway 不加过滤，业务服务会被 webhook 打爆。

**发生条件**：用户频繁进出协同会话。

**真实 callback 序列示例**：

```
用户A 打开       → status=1
用户B 加入       → status=1
用户C 加入       → status=1
用户A 离开       → status=1
用户B 修改后保存  → status=2  ← 只有这个需要触发 webhook
```

**解决方案**：Gateway 只对 `status=2`（可保存）和 `status=6`（强制保存）触发 webhook。status=1/3/4/7 只记日志，不通知业务服务。

```go
// callback handler 内部
switch body.Status {
case 2, 6:  // ready for saving / force saved
    go gateway.downloadAndReplace(body.URL)
    go gateway.fireWebhook(docID)
case 1:     // user connected/disconnected
    gateway.log("co-editing activity", body.Users)
case 4:     // closed with no changes
    // no-op
}
```

---

### 12.3 风险 2：Document Key 不一致 — 🔴 致命，必须保证

**现象**：两个用户打开"同一个文档"，但 Gateway 给了不同的 `document.key`，Document Server 会将其视为两个独立会话，**无法协同编辑**。

**原因**：`document.key` 是 Document Server 的会话标识——同一个 key = 同一个编辑会话。

**正常流程**：

```
服务上传文档 → Gateway 生成 document_id="doc_123"，key="Khirz6zTPdfd7"，持久化到 meta

用户A 打开 /edit?token=...&doc=123
  → Gateway 读 meta → key="Khirz6zTPdfd7" → 创建协同会话

用户B 打开同一个文档
  → Gateway 读 meta → key="Khirz6zTPdfd7" → 加入同一个协同会话 ✓
```

**关键约束**：Gateway 必须在首次上传时生成 key 并持久化到 meta.json，后续所有对该文档的 `/edit` 请求始终复用同一个 key。**禁止每次请求重新生成**。

```go
// Meta 结构 — editor_key 首次生成，终身不变
type Meta struct {
    DocumentID string `json:"document_id"`
    EditorKey  string `json:"editor_key"`  // ← 关键字段
    // ...
}
```

---

### 12.4 风险 3：TTL 不够长 — 🟡 中影响，配置解决

**现象**：团队协作断断续续改了两天，TTL 默认 8 小时不够。用户再次打开时发现文档已被清理。

**解决方案**：TTL 在编辑会话活跃时自动续期。每次收到 status=1（用户编辑中），将 expires_at 往后推 8 小时。

```go
// callback handler 收到 status=1 时
gateway.storage.ExtendTTL(docID, 8*time.Hour)
```

效果：只要文档还有人在编辑，就不会被清理。只有所有用户都关闭后，TTL 才开始倒计时。

---

### 12.5 风险 4：Callback 竞态 — 🟡 中影响，需 debounce

**现象**：协同编辑中，Document Server 可能短时间内连续发多次 status=2 callback。Gateway 如果每次 callback 都触发 webhook + 替换存储：

```
callback 1 (t=0ms)   → webhook 1 → 服务下载版本 1
callback 2 (t=150ms) → webhook 2 → 服务下载版本 2（覆盖了还没来得及处理的版本 1）
```

**影响**：服务如果在两次 webhook 之间正好在下载文件，可能拿到中间版本。

**解决方案**：Gateway 对同一个 document 的 callback 做 **debounce**（200ms 窗口内合并）：

```
callback 1 (status=2, t=0ms)   ─┐
callback 2 (status=2, t=150ms)  ─┼── debounce 200ms → 只处理最新的 callback
                                 ─┘   下载最新文件，只发一次 webhook
```

---

### 12.6 风险 5：多服务无法跨服务协同 — 🟢 设计如此

**现象**：服务 A 和服务 B 分别上传同一份 `合同.docx`，各自创建独立的 document_id 和 editor_key，两个编辑会话互不通。

**判断**：这不是 bug，是架构边界。Gateway 对两个服务的文件不做"是否是同一个"的语义判断。如果需要跨服务协同，应由上层（如共享文档库服务）统一上传，而不是各服务各自上传。

---

### 12.7 风险 6：编辑中服务提前拉取 — 🟢 设计容错

**现象**：用户正在编辑，webhook 还没发，业务服务提前调 `GET /api/v1/documents/{id}` 下载。

Gateway 行为：
```
is_edited == false → 返回 409 Conflict
  Body: {"status": "editing", "message": "文档编辑中"}

is_edited == true  → 返回最新文件
```

服务不会拿到未完成的文件。

---

### 12.8 协同编辑风险总结

| 风险 | 严重度 | 缓解 |
|---|---|---|
| Callback 风暴 | 🔴 高 | 仅 status=2/6 触发 webhook |
| Key 不一致导致无法协同 | 🔴 致命 | 首次生成 key 持久化到 meta，永不替换 |
| TTL 覆盖不到长编辑 | 🟡 中 | status=1 触发自动续期 |
| Callback 竞态 | 🟡 中 | 200ms debounce 去重 |
| 跨服务无法协同 | 🟢 设计边界 | 由上层共享文档服务统一上传 |
| 编辑中拉文件 | 🟢 容错 | 返回 409 告知编辑中 |


## 13. 附录
### A. 参考文档

- [ONLYOFFICE Docs API - 基本概念](https://api.onlyoffice.com/zh-CN/docs/docs-api/get-started/basic-concepts/)
- [ONLYOFFICE Docs API - 工作原理](https://api.onlyoffice.com/zh-CN/docs/docs-api/get-started/how-it-works/)
- [DocEditor 配置](https://api.onlyoffice.com/docs/docs-api/usage-api/doceditor/)
- [Callback Handler](https://api.onlyoffice.com/docs/docs-api/usage-api/callback-handler/)
- [配置概述](https://api.onlyoffice.com/docs/docs-api/usage-api/advanced-parameters/)

---

*文档版本：v1.0 · 2026-07-02*
