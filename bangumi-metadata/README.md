# bangumi-metadata

为 [fake-komga-115](https://github.com/xJogger/fake-komga-115) 提供 Bangumi 元数据刮削的独立服务。

**直接读取 fake-komga-115 的 SQLite 数据库**，新增元数据表，不修改原项目任何代码。

## 功能

- 搜索 Bangumi 匹配系列（按文件夹名）
- 刮削系列元数据：标题、作者、出版社、简介、标签、评分、状态
- 刮削卷元数据：卷号、ISBN、发售日、卷封面
- 支持 Bangumi Access Token（搜索 NSFW 条目）
- 单次刮削或常驻 HTTP 服务模式
- Web 管理页面

## 快速开始

### Docker Compose（与 fake-komga-115 共用数据卷）

```yaml
services:
  bangumi-metadata:
    image: bangumi-metadata:latest
    container_name: bangumi-metadata
    restart: unless-stopped
    ports:
      - "25601:25601"
    environment:
      FK115_DB_PATH: /data/fake-komga-115.db
      BANGUMI_ACCESS_TOKEN: ""  # 可选
      TZ: Asia/Shanghai
    volumes:
      - fake-komga-115-data:/data:ro

volumes:
  fake-komga-115-data:
    external: true
```

### 直接运行（需要 Go 1.23+）

```bash
# 构建
go build -o bangumi-metadata ./cmd/server

# 运行 HTTP 服务
./bangumi-metadata --db /path/to/fake-komga-115/data/fake-komga-115.db

# 或一次刮削后退出
./bangumi-metadata --db /path/to/fake-komga-115.db --scrape
```

## API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/scrape` | 开始刮削（仅未匹配的系列） |
| POST | `/api/scrape?force=true` | 强制全部重新刮削 |
| GET | `/api/status` | 刮削状态 |
| GET | `/api/stats` | 统计信息 |
| GET | `/api/series/{id}` | 查询系列元数据 |
| GET | `/` | 管理页面 |

## 环境变量

| 变量 | 说明 |
|------|------|
| `FK115_DB_PATH` | fake-komga-115 数据库路径（必填） |
| `BANGUMI_ACCESS_TOKEN` | Bangumi API Access Token（可选，用于 NSFW） |

## 数据模型

新增两个 SQLite 表（在 fake-komga-115 的数据库中）：

- `bangumi_series_meta` — 系列元数据（标题、作者、出版社、简介、标签、评分、封面等）
- `bangumi_book_meta` — 卷元数据（卷号、ISBN、发售日、封面等）

外键关联到原始 `series` 和 `books` 表，级联删除。
