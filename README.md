# ONLYOFFICE Gateway

> 将 ONLYOFFICE 文档编辑能力抽象为极简 HTTP API，业务服务零侵入接入。

[![Go](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go)](https://go.dev)
[![version](https://img.shields.io/badge/version-v0.1.0-blue)](VERSION)
[![tests](https://img.shields.io/badge/tests-22%20passing-brightgreen)](.)

## 解决的问题

ONLYOFFICE Docs API 要求每个接入服务自行处理：

- 文件下载端点（`document.url`）
- 回调端点（`callbackUrl`）
- JWT 签名/验签
- 编辑器前端集成（`api.js` + `DocsAPI.DocEditor`）

如果你的团队有 **N 个业务服务** 都需要文档编辑，每个都改一整套很痛苦。

**Gateway 收敛所有 ONLYOFFICE 协议细节**，业务服务只需 3 步：

```
1. POST 文件到 Gateway    → 拿到 document_id
2. <OnlyOfficeEditor />   → 一行组件嵌入编辑器
3. 收 Webhook → 下载结果   → 文件回到手里
```

## 架构

```
 业务服务 A/B/C                     Gateway                   Document Server
      │                                │                            │
      │── POST /api/v1/documents ─────→│                            │
      │   (multipart + JWT自签)         │── 生成 OO config ────────→│
      │                                │   callbackUrl = Gateway    │
      │  iframe /edit?token=JWT        │   document.url = Gateway   │
      │      ┌─────────────────────┐   │                            │
      │      │  DocsAPI.DocEditor  │   │                            │
      │      │  postMessage hooks  │   │                            │
      │      └─────────────────────┘   │                            │
      │                                │←── GET /download/{id} ─────│
      │                                │── 文件二进制 ──────────────→│
      │                                │                            │
      │                                │    用户编辑文档            │
      │                                │                            │
      │                                │←── POST /callback ────────│
      │                                │    status=2, url=...       │
      │←── Webhook 通知 ──────────────│                            │
      │── GET /api/v1/documents/{id}─→│                            │
      │←── 编辑结果 ─────────────────│                            │
```

## 快速开始

### 1. 配置

```bash
cp gateway.yaml.example gateway.yaml
```

编辑 `gateway.yaml`：

```yaml
listen_addr: "127.0.0.1:18080"
document_server_url: "https://your-document-server.com"
jwt_secret: "与 Document Server 一致的 JWT secret"
storage_dir: "./data/storage"
ttl_hours: 8
webhook_max_retries: 3

services:
  - id: "my-app"
    public_key: |
      -----BEGIN PUBLIC KEY-----
      ...
      -----END PUBLIC KEY-----
    allowed_webhook_domains:
      - "my-app.example.com"
```

### 2. 启动

```bash
make build && ./bin/gateway -config gateway.yaml
# Gateway v0.1.0 listening on 127.0.0.1:18080
```

### 3. 生成 RSA 密钥对（业务服务）

```bash
openssl genpkey -algorithm RSA -out private.pem -pkeyopt rsa_keygen_bits:2048
openssl rsa -pubout -in private.pem -out public.pem
```

将 `public.pem` 内容提供给 Gateway 管理员，写入 `gateway.yaml`。

### 4. 上传文档

```bash
# 1. 用私钥签 JWT
TOKEN=$(node -e "
  const jwt = require('jsonwebtoken');
  const fs = require('fs');
  const key = fs.readFileSync('private.pem');
  console.log(jwt.sign({
    service_id: 'my-app',
    webhook_url: 'https://my-app.example.com/callback',
    file_name: 'report.docx',
    document_type: 'word',
    branding: { logo_url: 'https://my-app.example.com/logo.png', language: 'zh-CN' }
  }, key, { algorithm: 'RS256', expiresIn: '60s' }));
")

# 2. 上传
curl -X POST http://localhost:18080/api/v1/documents \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@report.docx"
# → {"document_id":"doc_20260702abc123","status":"uploaded","expires_at":"2026-07-02T22:00:00Z"}
```

### 5. 嵌入编辑器

```tsx
import { OnlyOfficeEditor } from "@zenmind/onlyoffice-editor";

<OnlyOfficeEditor
  documentId="doc_20260702abc123"
  gatewayUrl="http://localhost:18080"
  onReady={() => console.log("编辑器就绪")}
  onSaved={(event) => fetch(`/api/download?doc=${event.document_id}`)}
  onError={(err) => console.error(err)}
/>
```

## API

### 对外 API（业务服务调用）

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/api/v1/documents` | 上传文档 |
| `GET` | `/api/v1/documents/{id}` | 下载编辑结果 |
| `DELETE` | `/api/v1/documents/{id}` | 手动清理文档 |
| `GET` | `/api/v1/health` | 健康检查 |
| `GET` | `/edit` | 编辑器 HTML 页面（iframe 使用） |

### 内部 API（Document Server 专用）

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/download/{docId}` | Document Server 下载原始文件 |
| `POST` | `/callback` | Document Server 回调 |

### POST /api/v1/documents

上传文档到 Gateway。

**Headers**:
- `Authorization: Bearer <JWT>`（RS256，服务私钥自签）
- `Content-Type: multipart/form-data`

**Body**:
- `file`：文件二进制
- `meta`：JSON 字符串（可选，覆盖 JWT 中的元数据）

**JWT Claims**:

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `service_id` | string | ✅ | 服务标识 |
| `webhook_url` | string | ✅ | 编辑完成回调地址，域名需在白名单内 |
| `external_id` | string | | 业务侧文档 ID |
| `user.id` | string | | 编辑者 ID |
| `user.name` | string | | 编辑者名称 |
| `file_name` | string | | 文件名 |
| `document_type` | string | | `word` / `cell` / `slide` / `pdf` |
| `branding` | object | | 编辑器品牌定制 |
| `config_overrides` | object | | 完全定制（高级） |
| `exp` | number | ✅ | JWT 过期时间（建议 60s） |

**branding 字段**:

| 字段 | 映射到 ONLYOFFICE config |
|---|---|
| `logo_url` | `editorConfig.customization.logo.image` |
| `language` | `editorConfig.lang` |
| `color_theme` | `editorConfig.customization.colors` |

### 编辑器定制

Gateway 通过**三层 merge** 构建 ONLYOFFICE config：

```
Layer 1: Gateway 默认值（必填字段）
Layer 2: branding（品牌定制：logo、语言、主题色）
Layer 3: config_overrides（完全穿透覆盖）
```

优先级 Layer 3 > Layer 2 > Layer 1。

### Webhook 通知

```
POST <webhook_url>
X-Gateway-Event: document.saved
X-Gateway-Signature: sha256=<HMAC-SHA256(url+body, gateway_jwt_secret)>

{
  "event": "document.saved",
  "document_id": "doc_20260702abc123",
  "external_id": "contract-2024-001",
  "status": "ready",
  "file_type": "docx",
  "file_size_bytes": 45678,
  "edited_at": "2026-07-02T14:30:00Z"
}
```

## 协同编辑

Gateway 不破坏协同编辑。Document Server 的 Operational Transformation 引擎负责所有实时同步逻辑。

| 风险 | 缓解 |
|---|---|
| Callback 风暴（多人频繁进出） | 仅 status=2/6 触发 webhook |
| document.key 不一致导致无法协同 | 首次生成后持久化，所有请求复用同一个 key |
| TTL 在长编辑会话中过期 | status=1 自动续期 |
| Callback 竞态（短时间内多次保存） | 200ms debounce 去重 |

详见 [ONLYOFFICE Gateway — 项目计划任务书](docs/ONLYOFFICE%20Gateway%20—%20项目计划任务书.md)。

## 部署

### 本地开发

```bash
make run
```

### Docker

```bash
docker build -t onlyoffice-gateway .
docker run -p 18080:18080 \
  -v $(pwd)/gateway.yaml:/app/gateway.yaml:ro \
  -v gateway-data:/app/data \
  onlyoffice-gateway
```

### Document Server 本机 Docker 配置

如果 Document Server 以 Docker 容器运行在本机，Gateway 需绑定 `0.0.0.0`（而非 `127.0.0.1`），使容器能通过 `host.docker.internal` 访问 Gateway：

```yaml
listen_addr: "0.0.0.0:18080"
document_server_url: "http://localhost:8080"
```

## 开发

```bash
# 安装依赖
go mod tidy

# 运行测试（Go + 前端）
make test
cd frontend-sdk && npm test

# 同步版本
make sync-version

# 构建
make build
```

### 技术栈

| 层 | 技术 |
|---|---|
| Gateway 后端 | Go 1.22+, 标准库 HTTP router |
| 存储 | 本地磁盘（接口预留 S3/MinIO） |
| 认证 | JWT RS256（服务自签） + HMAC（webhook 签名） |
| 前端 SDK | React 18, TypeScript, Vitest + jsdom |

### 项目结构

```
.
├── cmd/gateway/main.go           # 入口
├── internal/
│   ├── config/config.go          # YAML 配置 + 公钥解析
│   ├── configbuilder/builder.go  # OO config 分层 merge
│   ├── gateway/server.go         # HTTP router
│   ├── handler/
│   │   ├── upload.go             # POST /api/v1/documents
│   │   ├── download.go           # GET /api/v1/documents/{id}
│   │   ├── callback.go           # POST /callback + webhook
│   │   ├── editor.go             # GET /edit (HTML 页面)
│   │   └── helpers.go            # 工具函数
│   ├── jwt/jwt.go                # RS256 验签
│   ├── storage/
│   │   ├── interface.go          # Store 接口（预留 S3）
│   │   └── local.go              # 本地磁盘实现
│   └── version/version.go        # 构建版本
├── frontend-sdk/                 # npm 包 @zenmind/onlyoffice-editor
│   └── src/OnlyOfficeEditor.tsx
├── gateway.yaml.example          # 配置模板
├── VERSION                       # 版本号
├── Makefile
├── Dockerfile
└── docs/
    └── ONLYOFFICE Gateway — 项目计划任务书.md
```

### 测试

```
$ go test ./... -count=1
ok  cmd/gateway                (1 test)
ok  internal/configbuilder      (3 tests)
ok  internal/gateway           (17 tests)

$ cd frontend-sdk && npm test
 ✓ src/OnlyOfficeEditor.test.tsx  (6 tests)
```

## License

MIT
