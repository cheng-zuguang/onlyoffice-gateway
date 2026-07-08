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
cp .env.example .env
```

编辑 `.env`：

```env
# Gateway
LISTEN_ADDR=0.0.0.0:18080
DOCUMENT_SERVER_URL=http://localhost:18000
JWT_SECRET=                          # 与 Document Server 一致
STORAGE_DIR=./data/storage
TTL_HOURS=8
WEBHOOK_MAX_RETRIES=3

# Admin Panel
ADMIN_USERNAME=admin
ADMIN_PASSWORD=admin123              # 生产环境务必修改
SERVICE_STORE_PATH=./data/services.json
```

Gateway 核心配置通过 `.env` 环境变量注入。全部配置通过 `.env` 环境变量注入。

### 2. 启动

```bash
# 启动网关
make run

# 另开终端，启动管理端前端
make frontend-dev
```

### 3. 通过管理端注册业务服务

打开 `http://localhost:5173/admin/login`，登录后：

1. 点击 **Add Service**
2. 填入 Service ID、RSA 公钥（PEM 格式）、Webhook 域名白名单
3. 提交后立即生效，无需重启 Gateway

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

### Admin API（管理端）

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| `POST` | `/admin/api/login` | 无 | 管理员登录，返回 JWT |
| `GET` | `/admin/api/services` | Bearer | 列出所有业务服务 |
| `POST` | `/admin/api/services` | Bearer | 新增业务服务 |
| `PUT` | `/admin/api/services/{id}` | Bearer | 更新业务服务 |
| `DELETE` | `/admin/api/services/{id}` | Bearer | 删除业务服务 |

Admin API 使用 HMAC-SHA256 JWT 认证（复用 `JWT_SECRET`），24h 过期。

### POST /api/v1/documents

**Headers**:
- `Authorization: Bearer <JWT>`（RS256，服务私钥自签）
- `Content-Type: multipart/form-data`

**Body**: `file`（文件二进制）

**JWT Claims**:

| 字段 | 必填 | 说明 |
|---|---|---|
| `service_id` | ✅ | 服务标识 |
| `webhook_url` | ✅ | 编辑完成回调地址 |
| `external_id` | | 业务侧文档 ID |
| `file_name` | | 文件名 |
| `document_type` | | `word` / `cell` / `slide` / `pdf` |
| `branding` | | 编辑器品牌定制（logo、语言、主题色） |
| `config_overrides` | | ONLYOFFICE config 完全覆盖 |
| `exp` | ✅ | 上传 token 有效期，控制调用 API 的时间窗口。生成后应立即使用，建议 60s 以防范重放 |

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
X-Gateway-Signature: sha256=<HMAC(url+body, jwt_secret)>

{ "event": "document.saved", "document_id": "doc_xxx", "status": "ready" }
```

Gateway 对 Webhook 做有限重试（3 次，指数退避），失败后静默。

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
# 编辑 .env，至少设置 JWT_SECRET 和 ADMIN_PASSWORD
docker compose up -d
```

管理端 build 产物位于 `admin-ui/dist/`，可部署到任意静态文件服务器或 CDN。Vite dev server 代理 `/admin/api` 到 Gateway 后端。

### 生产部署

网关配置全部通过 `.env` 环境变量注入：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `LISTEN_ADDR` | `:18080` | 监听地址 |
| `DOCUMENT_SERVER_URL` | — | Document Server 地址 |
| `JWT_SECRET` | — | 与 Document Server 共用 |
| `STORAGE_DIR` | `./data/storage` | 文件存储路径 |
| `TTL_HOURS` | `8` | 文档存活时间 |
| `WEBHOOK_MAX_RETRIES` | `3` | Webhook 最大重试次数 |
| `ADMIN_USERNAME` | `admin` | 管理端用户名 |
| `ADMIN_PASSWORD` | — | **必须设置** |
| `SERVICE_STORE_PATH` | `./data/services.json` | Service 持久化文件 |

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
| 认证 | JWT RS256（服务自签）+ HMAC（webhook 签名 + admin session） |
| 存储 | 本地磁盘（接口预留 S3/MinIO） |
| 管理端 | React 18 + Vite + shadcn/ui |
| SDK | React 18, TypeScript, Vitest + jsdom |

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
