# ONLYOFFICE Gateway

> 将 ONLYOFFICE 文档编辑能力抽象为极简 HTTP API，业务服务零侵入接入。

[![Go](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go)](https://go.dev)
[![version](https://img.shields.io/badge/version-v0.1.0-blue)](VERSION)

## 解决的问题

ONLYOFFICE Docs API 要求每个接入服务自行处理：

- 文件下载端点（`document.url`）
- 回调端点（`callbackUrl`）
- JWT 签名/验签
- 编辑器前端集成（`api.js` + `DocsAPI.DocEditor`）

如果你的团队有 **N 个业务服务** 都需要文档编辑，每个都改一整套很痛苦。

**Gateway 收敛所有 ONLYOFFICE 协议细节**，业务服务只需 3 步：

```
1. POST 文件或 source_url 到 Gateway → 拿到 document_id
2. <OnlyOfficeEditor />   → 一行组件嵌入编辑器
3. 收 Webhook → 下载结果   → 文件回到手里
```

## 架构

Gateway 是唯一的 ONLYOFFICE 集成边界，不按业务形态拆成两套架构。桌面端、本地文件型服务和线上已托管文件型服务都注册到同一个 Gateway；差异只体现在创建编辑会话时选择哪种文件接入模式。

```
 业务服务 A/B/C                         Gateway                  Document Server
      │                                    │                           │
      │── POST /api/v1/documents ─────────→│                           │
      │   A: multipart 文件上传             │── 生成 OO config ────────→│
      │   B: source_url 元数据直连          │   callbackUrl = Gateway   │
      │                                    │   document.url = Gateway  │
      │  iframe /edit?token=JWT            │        或 source_url      │
      │      ┌─────────────────────┐       │                           │
      │      │  DocsAPI.DocEditor  │       │                           │
      │      │  postMessage hooks  │       │                           │
      │      └─────────────────────┘       │                           │
      │                                    │←── GET document.url ──────│
      │                                    │── 文件二进制 ─────────────→│
      │                                    │                           │
      │                                    │    用户编辑文档           │
      │                                    │                           │
      │                                    │←── POST /callback ───────│
      │                                    │    status=2, url=...      │
      │←── Webhook 通知 ──────────────────│                           │
      │── 保存编辑结果到业务侧文件系统/对象存储                         │
```

两类典型接入方：

| 业务形态 | 文件现状 | 推荐接入模式 | 说明 |
|---|---|---|---|
| 桌面端或本地工作区服务 | 文件只有本地路径，没有公网 URL | multipart 上传托管 | 业务服务主动把文件推到 Gateway，Gateway 临时托管原文件和编辑后文件。 |
| 线上部署服务 | 文件可通过 HTTPS 域名访问 | `source_url` 直连 | 业务服务只把短时效 HTTPS 读取地址交给 Gateway，原文件和最终保存仍由业务服务自己管理。 |
| 多个业务服务并存 | 各自管理自己的文档 | 同一个 Gateway，多服务注册 | 每个服务用自己的 `service_id`、RSA 公钥、webhook 白名单和回写逻辑隔离。 |

## 快速开始

### 1. 配置

```bash
cp .env.example .env
make init-secrets
```

编辑 `.env`：

```env
# Gateway
LISTEN_ADDR=0.0.0.0:18080
DOCUMENT_SERVER_URL=http://localhost:18000
DOCUMENT_SERVER_JWT_SECRET=          # 仅与 Document Server 一致
GATEWAY_ADMIN_SESSION_SECRET=        # 仅管理端 Session JWT
GATEWAY_CALLBACK_CAPABILITY_SECRET=  # 仅 Document Server callback capability
WEBHOOK_SECRET_ENCRYPTION_KEY=       # Base64 编码的 32 字节 AES 密钥
STORAGE_DIR=./data/storage
TTL_HOURS=8
CLEANUP_INTERVAL=1h
WEBHOOK_MAX_RETRIES=3
CALLBACK_QUEUE_SIZE=64
CALLBACK_WORKERS=4

# Admin Panel
ADMIN_USERNAME=admin
ADMIN_PASSWORD=admin123              # 生产环境务必修改
SERVICE_STORE_PATH=./data/services.json
```

Gateway 核心配置可通过 `.env` 环境变量注入，也可通过启动参数指定 `gateway.yaml`。

使用 `gateway.yaml` 时，清理间隔使用相同的 Go 时长格式：

```yaml
cleanup_interval: 15m
callback_queue_size: 64
callback_workers: 4
```

### 2. 启动

```bash
# 启动网关
make run

# 另开终端，启动管理端前端
make frontend-dev
```

### 3. 通过管理端注册业务服务

打开 `http://localhost:16666/admin/login`，登录后：

1. 点击 **Add Service**
2. 填入 Service ID、RSA 公钥（PEM 格式）、Webhook 域名白名单
3. 提交后立即复制只展示一次的 Webhook Secret，并配置到对应业务服务
4. 服务注册立即生效，无需重启 Gateway

### 4. 生成 RSA 密钥对（业务服务）

```bash
openssl genpkey -algorithm RSA -out private.pem -pkeyopt rsa_keygen_bits:2048
openssl rsa -pubout -in private.pem -out public.pem
```

将 `public.pem` 的内容粘贴到管理端 "RSA Public Key" 字段。

### 5. 上传文档

```bash
TOKEN=$(node -e "
  const jwt = require('jsonwebtoken');
  const fs = require('fs');
  const key = fs.readFileSync('private.pem');
  console.log(jwt.sign({
    service_id: 'my-app',
    webhook_url: 'https://my-app.example.com/callback',
    file_name: 'report.docx',
    document_type: 'word',
  }, key, { algorithm: 'RS256', expiresIn: '60s' }));
")

curl -X POST http://localhost:18080/api/v1/documents \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@report.docx"
# → {"document_id":"doc_xxx","status":"uploaded"}
```

### 6. 嵌入编辑器

```tsx
import { OnlyOfficeEditor } from "@zenmind/onlyoffice-editor";

<OnlyOfficeEditor
  documentId="doc_xxx"
  gatewayUrl="http://localhost:18080"
  token="<edit JWT>"
  onReady={() => console.log("就绪")}
  onSaved={(e) => fetch(`/api/download?doc=${e.document_id}`)}
/>
```

## API

### 业务 API

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/api/v1/documents` | 上传文档（JWT RS256 自签） |
| `GET` | `/api/v1/documents/{id}` | 下载编辑结果 |
| `GET` | `/api/v1/health` | 健康检查 |
| `GET` | `/api/v1/health/ds` | Document Server 连通性检查 |
| `GET` | `/edit` | 编辑器 HTML 页面（iframe） |

### Document Server 内部 API

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/download/{docId}` | Document Server 下载原始文件 |
| `POST` | `/callback` | Document Server 回调 |

`/download/{docId}` 始终返回上传时的原始文件，不会因后续编辑结果覆盖而改变。Gateway 使用标准文件响应能力返回 `Content-Length`、`ETag`、`Last-Modified`、`Accept-Ranges`，并设置 `Cache-Control: private, max-age=28800`，便于 Document Server 更快获取和复用同一编辑会话的原文件。

### Admin API（管理端）

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| `POST` | `/admin/api/login` | 无 | 管理员登录，返回 JWT |
| `GET` | `/admin/api/services` | Bearer | 列出所有业务服务 |
| `POST` | `/admin/api/services` | Bearer | 新增业务服务 |
| `PUT` | `/admin/api/services/{id}` | Bearer | 更新业务服务 |
| `DELETE` | `/admin/api/services/{id}` | Bearer | 删除业务服务 |
| `POST` | `/admin/api/services/{id}/webhook-secret/rotate` | Bearer | 生成待切换凭证，只返回一次明文 |
| `POST` | `/admin/api/services/{id}/webhook-secret/activate` | Bearer | 激活待切换凭证 |
| `POST` | `/admin/api/services/{id}/webhook-secret/rollback` | Bearer | 十分钟窗口内回滚凭证 |

Admin API 使用 `GATEWAY_ADMIN_SESSION_SECRET` 签发 HMAC-SHA256 Session JWT，24h 过期。创建服务和轮换凭证时返回的 service webhook secret 只展示一次；业务服务只保存自己的 secret，不应获得 `DOCUMENT_SERVER_JWT_SECRET`。

### POST /api/v1/documents

Gateway 支持两种文件接入模式；同一服务可按文档选择使用。模式是文件进入编辑会话的方式，不是两套 Gateway 架构。

| 场景 | 推荐模式 | Gateway 是否保存文件 | 结果获取方式 |
|---|---|---|---|
| 桌面端、本地工作区、无公网 HTTPS 文件地址 | multipart 上传托管 | 保存原文件和编辑后文件，按 TTL 清理 | webhook 后调用 `GET /api/v1/documents/{id}`，再写回业务侧文件系统或存储 |
| 线上部署服务，文件已有公网 HTTPS 读取地址 | `source_url` 直连 | 只保存元数据，不保存文件字节 | webhook 中读取 `edited_url` 并立即保存回业务侧存储 |
| 本地/MinIO 只能提供 HTTP URL | multipart 上传托管 | 保存原文件和编辑后文件 | 避免把 HTTP MinIO URL 放入 `source_url` |

#### 模式 A：上传托管（multipart）

**Headers**:
- `Authorization: Bearer <JWT>`（RS256，服务私钥自签）
- `Content-Type: multipart/form-data`

**Body**: `file`（文件二进制）

**JWT Claims**:

| 字段 | 必填 | 说明 |
|---|---|---|
| `service_id` | ✅ | 服务标识 |
| `webhook_url` | | 编辑完成回调地址；提供时该 service 必须已有 active webhook credential，否则返回 422；省略时不会投递业务 webhook |
| `external_id` | | 业务侧文档 ID |
| `file_name` | | 文件名 |
| `document_type` | | `word` / `cell` / `slide` / `pdf` |
| `branding` | | 编辑器品牌定制（logo、语言、主题色） |
| `config_overrides` | | ONLYOFFICE config 完全覆盖 |
| `exp` | ✅ | 上传 token 有效期，控制调用 API 的时间窗口。生成后应立即使用，建议 60s 以防范重放 |

#### 模式 B：业务 URL 直连（`source_url`）

适用于业务服务已管理文件，并且能提供 Document Server 可访问的短时效 HTTPS **GET** URL 的场景。该 URL 可以来自业务域名、对象存储或预签名地址；放入已签名 JWT 的 `source_url` claim，并以空请求体调用同一端点。Gateway 只保存编辑会话元数据，不接收、复制或清理原文件；ONLYOFFICE 直接下载该 URL。

```javascript
const token = jwt.sign({
  service_id: 'my-app',
  webhook_url: 'https://my-app.example.com/callback',
  source_url: 'https://bucket.s3.example.com/report.docx?...',
  file_name: 'report.docx',
  document_type: 'word',
}, privateKey, { algorithm: 'RS256', expiresIn: '60s' });

await fetch(`${gatewayUrl}/api/v1/documents`, {
  method: 'POST', headers: { Authorization: `Bearer ${token}` },
});
```

`source_url` 必须覆盖编辑会话期间 Document Server 的读取需求，必须是 HTTPS URL，且不可包含用户名/密码或指向仅浏览器可访问的内网地址。业务服务负责原文件和最终文件的生命周期策略；Gateway 在该模式下只保存会话元数据，不会下载或清理业务侧对象。

### 编辑器定制

三层 merge 构建 ONLYOFFICE config：

```
Layer 1: Gateway 默认值（必填字段）
Layer 2: branding（logo、语言、主题色）
Layer 3: config_overrides（完全穿透覆盖）
```

### Webhook

```
POST <webhook_url>
X-Gateway-Event: document.saved
X-Gateway-Service-Id: <service_id>
X-Gateway-Timestamp: <unix-seconds>
X-Gateway-Delivery-Id: <stable-delivery-id>
X-Gateway-Signature: v1=<lowercase-hex-hmac-sha256>

{ "event": "document.saved", "document_id": "doc_xxx", "status": "ready", "edited_url": "..." }
```

签名原文是 `v1\n<service_id>\n<timestamp>\n<delivery_id>\n<raw_body>`，密钥是该 service 独立的 webhook secret。业务服务应在解析 JSON 前对原始 body 验签，并限制时间戳偏差（建议 300 秒）。

在 `source_url` 直连模式下，`edited_url` 是 ONLYOFFICE 提供的短时效下载地址；Gateway 不读取其内容，业务服务必须在收到 webhook 后立即下载并保存回自己的文件系统或对象存储。在 multipart 模式下，该字段为空，业务侧仍可使用 `GET /api/v1/documents/{id}` 获取 Gateway 保存的结果。

直连模式下业务 webhook 的推荐处理流程：

```javascript
app.post('/onlyoffice/webhook', async (req, res) => {
  verifyGatewaySignature(req);
  const { document_id, external_id, edited_url } = req.body;
  if (edited_url) {
    const edited = await fetch(edited_url);
    if (!edited.ok) throw new Error(`download edited file failed: ${edited.status}`);
    await saveToBusinessStorage(external_id || document_id, edited.body);
  } else {
    await copyFromGatewayResult(document_id);
  }
  res.sendStatus(204);
});
```

Gateway 对 Webhook 做有限重试（默认最多重试 3 次，即首次投递之外最多再尝试 3 次），退避附加短 jitter。网络错误、408、429、5xx 会重试，其他 4xx 视为永久失败；最终失败会记录指标和日志。保存任务会先进入有界队列，队列大小由 `CALLBACK_QUEUE_SIZE` 控制，worker 数由 `CALLBACK_WORKERS` 控制。收到 `SIGTERM` 或 `SIGINT` 后，Gateway 会先停止 HTTP 接入，再停止本地清理调度并排空已入队的保存任务。

保存回调中，Gateway 会从 Document Server 回调体的 `url` 下载编辑后文件并保存为最新版。该下载和 Webhook 投递共用带连接池的 HTTP client，支持 keep-alive 和连接复用，降低高并发保存时的连接建立开销。

## 性能与缓存

- Document Server 前端资源代理会为 `/web-apps/`、`/sdkjs/`、`/spellchecker/`、`/cache/` 等静态资源设置缓存头；版本化资源使用 `public, max-age=31536000, immutable`。
- `/download/{docId}` 面向 Document Server 返回原始文件，支持 Range 请求和条件请求；条件请求会先读取对象元信息，命中 304 或非法 Range 时不会下载对象正文。业务侧 `/api/v1/documents/{id}` 仍返回编辑完成后的最新版。
- 本地 storage 采用按文档 ID 的锁粒度，不同文档的上传、读取、保存不会被单个文档的大文件写入全局阻塞。
- `/api/v1/metrics` 以 Prometheus text exposition 格式暴露 callback 保存排队/丢弃/成功/失败次数和 webhook 成功/失败次数，包含 `HELP` 与 `TYPE` 元信息。

## 部署

### 本地开发

```bash
# 终端 1: 启动网关
make run

# 终端 2: 启动管理端
make frontend-dev
```

### Docker Compose

```bash
cp .env.example .env
make init-secrets
# 编辑 .env，至少设置 ADMIN_PASSWORD；init-secrets 不覆盖已有值
docker compose up -d
```

管理端 build 产物位于 `admin-ui/dist/`，可部署到任意静态文件服务器或 CDN。Vite dev server 代理 `/admin/api` 到 Gateway 后端。

### 生产部署

网关配置全部通过 `.env` 环境变量注入：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `LISTEN_ADDR` | `:18080` | 监听地址 |
| `DOCUMENT_SERVER_URL` | — | Document Server 地址 |
| `DOCUMENT_SERVER_JWT_SECRET` | — | 仅 Gateway 与 Document Server 共用的编辑器配置 JWT secret |
| `GATEWAY_ADMIN_SESSION_SECRET` | — | 仅 Gateway 管理端 Session JWT 使用 |
| `GATEWAY_CALLBACK_CAPABILITY_SECRET` | — | 仅 Gateway callback capability 使用 |
| `WEBHOOK_SECRET_ENCRYPTION_KEY` | — | Base64 编码的 32 字节 AES-256-GCM 主密钥，用于加密 service webhook secret |
| `STORAGE_BACKEND` | `local` | 文件存储后端：`local` / `s3` |
| `STORAGE_DIR` | `./data/storage` | 文件存储路径 |
| `S3_ENDPOINT` | — | S3/MinIO endpoint，MinIO 示例：`http://minio:9000` |
| `S3_REGION` | `us-east-1` | S3 region |
| `S3_BUCKET` | — | S3/MinIO bucket（需预先创建） |
| `S3_ACCESS_KEY` | — | S3/MinIO access key |
| `S3_SECRET_KEY` | — | S3/MinIO secret key |
| `S3_USE_PATH_STYLE` | `true` | MinIO 通常需要设为 `true` |
| `S3_USE_SSL` | `true` | endpoint 未带 scheme 时是否默认使用 HTTPS |
| `S3_PREFIX` | — | 对象 key 前缀，例如 `documents` |
| `TTL_HOURS` | `8` | 文档存活时间 |
| `CLEANUP_INTERVAL` | `1h` | 本地存储过期文档清理间隔，仅 `STORAGE_BACKEND=local` 生效；环境变量和 YAML 均支持 `15m`、`1h` 格式 |
| `WEBHOOK_MAX_RETRIES` | `3` | Webhook 在首次投递之外的最大重试次数 |
| `CALLBACK_QUEUE_SIZE` | `64` | 保存回调有界队列长度 |
| `CALLBACK_WORKERS` | `4` | 保存回调并发 worker 数 |
| `ADMIN_USERNAME` | `admin` | 管理端用户名 |
| `ADMIN_PASSWORD` | — | **必须设置** |
| `SERVICE_STORE_PATH` | `./data/services.json` | Service 持久化文件，Admin API 会校验 RSA 公钥并原子写入 |

### 访问日志

Gateway 对业务 API、Document Server 内部 API、编辑器页面、健康检查和 Admin API 统一输出访问日志：

```text
[http] POST /admin/api/login 200 1.2ms remote_addr=127.0.0.1:54321 user_agent="curl/8.0.1" request_id=req-123
```

字段依次为：HTTP method、request URI、status、duration、remote address、User-Agent、request id。`request_id` 读取 `X-Request-Id` / `X-Request-ID`，没有时为 `-`。

## 开发

```bash
make test             # Go 测试
make build            # 构建 Go binary
make frontend-build   # 构建管理端 SPA
make frontend-dev     # 启动管理端 dev server
```

### 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.22+, 标准库 HTTP router |
| 认证 | JWT RS256（服务自签）+ 分离的 Document Server JWT、callback capability、admin session 与每服务 webhook HMAC |
| 存储 | 本地磁盘 / S3-compatible 对象存储（MinIO、AWS S3） |
| 管理端 | React 18 + Vite + shadcn/ui |
| SDK | React 18, TypeScript, Vitest + jsdom |

### MinIO 本地开发

默认 `docker compose up -d` 仍使用本地磁盘。需要验证对象存储时：

```bash
STORAGE_BACKEND=s3 docker compose --profile minio up -d
```

MinIO console 默认暴露在 `http://localhost:19001`，bucket 默认为 `onlyoffice`，对象前缀默认为 `documents/`。

### 项目结构

```
.
├── cmd/gateway/                   # 入口
├── internal/
│   ├── admin/                     # Admin API（登录、Service CRUD、持久化）
│   ├── config/                    # 配置加载（YAML + 环境变量）
│   ├── configbuilder/             # ONLYOFFICE config 分层 merge
│   ├── gateway/                   # HTTP router + ServiceResolver 接口
│   ├── handler/                   # 业务 API handlers
│   ├── jwt/                       # JWT RS256 验签
│   ├── storage/                   # 文件存储接口 + 本地实现
│   └── version/                   # 构建版本
├── admin-ui/                      # 管理端 SPA（Vite + shadcn/ui）
│   └── src/
│       ├── pages/LoginPage.tsx
│       ├── pages/ServicesPage.tsx
│       ├── lib/api.ts             # API 客户端
│       └── components/ui/         # shadcn/ui 组件
├── frontend-sdk/                  # npm 包 @zenmind/onlyoffice-editor
├── .env.example                   # 环境变量模板
├── Makefile
├── Dockerfile
└── docs/
```

### 测试

```
$ go test ./... -count=1
ok  cmd/gateway                (1 test)
ok  internal/admin             (13 tests)
ok  internal/configbuilder     (3 tests)
ok  internal/gateway           (17 tests)
```

## License

MIT
