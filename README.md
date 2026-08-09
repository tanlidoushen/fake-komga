# fake-komga

轻量级 Komga 兼容服务，Go 实现，低资源占用。为 Mihon/Komikku/Tachiyomi 系软件提供漫画阅读服务。

## 项目结构

```
fake-komga/
├── fake-komga-115/         # 115 网盘后端（forked from xJogger/fake-komga-115）
│                            # 新增：bangumi_series_meta 元数据注入、booksMetadata 字段补全
│                            # 原项目：https://github.com/xJogger/fake-komga-115
├── fake-komga-local/       # 本地文件系统后端（基于 fake-komga 框架）
│                            # 支持本地目录扫描、阅读进度同步、元数据注入
├── bangumi-metadata/       # Bangumi 元数据刮削器
│                            # 手动匹配/批量刮削/正则提取关键词
│                            # 写入同一数据库，fake-komga 自动读取注入
└── deploy/                 # 部署配置
    ├── docker-compose.yml
    └── .env.example
```

## 架构

```
┌───────────────┐     ┌───────────────────┐     ┌──────────────┐
│  Mihon /      │────▶│  fake-komga-115   │────▶│  115 网盘    │
│  Komikku /    │     │  (端口 25602)      │     │  Open API    │
│  Tachiyomi    │     ├───────────────────┤     └──────────────┘
│               │────▶│  fake-komga-local │────▶│  本地漫画目录 │
│  (Komga 扩展)  │     │  (端口 25604)      │     └──────────────┘
└───────────────┘     └────────┬──────────┘
                               │ 共享数据库
                         ┌────▼──────────┐
                         │ bangumi-      │
                         │ metadata      │
                         │ (端口 25601)   │
                         │ 元数据刮削器   │
                         └───────────────┘
```

## 背景

Komga 官方版（Java）资源占用较高，在低配设备（如 NAS、开发板）上运行吃力。  
本项目用 Go 重写，资源占用低（约 10-30MB 内存），同时保持 Komga API 兼容，可无缝对接 Mihon/Komikku/Tachiyomi 的 Komga 扩展。

## 功能

### fake-komga-115
- 以 115 网盘为存储后端，通过 115 Open API 读取漫画
- Komga API 兼容（series/books/pages/thumbnail）
- 阅读进度同步（GET/PATCH/DELETE read-progress）
- 元数据注入（从 bangumi_series_meta 表读取标题/简介/标签/评分/作者/出版社）
- 搜索（COLLATE NOCASE + 特殊字符转义）
- 全文搜索（文件名/系列名）
- 支持分页、排序、库过滤、阅读状态过滤

### fake-komga-local
- 本地文件系统为存储后端，直接扫描目录
- 同上的完整 Komga API 兼容
- 扫描追踪（scan_runs 表记录每次扫描结果）
- 变更检测（file_modified_at + UPSERT）
- 删除检测（seen_scan_id 标记未见的条目自动清理）
- 标签/分类/出版社/作者过滤
- 元数据注入（与 bangumi-metadata 共享数据库）

### bangumi-metadata
- Bangumi 元数据手动匹配（搜索→确认）
- 批量自动刮削（全部/已选系列）
- 正则提取关键词（可自定义提取规则）
- 系列详情编辑（标题/简介/标签/作者/封面/出版社）
- 封面管理（生成/删除缩略图）
- 不覆盖已有元数据（可选择保留 fake-komga 自生成封面）

## 快速开始

### 前置要求

- Docker & Docker Compose
- 漫画目录（本地模式）或 115 网盘账号（115 模式）

### 本地模式（推荐）

```bash
# 1. 克隆
git clone https://github.com/tanlidoushen/fake-komga.git
cd fake-komga/deploy

# 2. 配置
cp .env.example .env
# 编辑 .env，设置 BANGUMI_ACCESS_TOKEN（刮削需要）
# 编辑 docker-compose.yml，修改 COMICS_DIR 为你本地的漫画目录路径

# 3. 启动
docker compose up -d

# 4. 访问
# fake-komga-local API:  http://localhost:25604
# bangumi-metadata 管理页: http://localhost:25601
# Mihon/Komikku 添加服务器: http://<你的IP>:25604
```

### 115 模式

```bash
# 需要 115 网盘账号和 Open API 凭证
# 在 fake-komga-115 管理页配置 refresh_token 和 access_token
# 然后启动 115 后端
docker compose -f docker-compose.yml -f docker-compose.115.yml up -d
```

## 配置说明

### 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `BANGUMI_ACCESS_TOKEN` | Bangumi API 令牌（刮削需要） | - |
| `COMICS_DIR` | 本地漫画目录路径 | `/comics` |
| `HTTP_PROXY` | 代理地址（用于访问 Bangumi API） | - |
| `TZ` | 时区 | `Asia/Shanghai` |

### 漫画目录结构

```
漫画目录/
├── [作者][系列名][卷号][状态][出版源]/
│   ├── 系列名 Vol.01.zip
│   ├── 系列名 Vol.02.zip
│   └── ...
├── [作者][另一系列].../
│   ├── Vol.01.zip
│   └── ...
└── ...
```

每个子目录 = 一个系列，支持 `.zip` / `.cbz` / `.rar` / `.cbr` 格式。

## ⚠️ 重要提示

**当前为测试版本，存在以下限制：**

- ❌ **无任何鉴权机制**（无登录、无 API Key、无 JWT）
- ❌ **不适用于公网部署**
- ❌ 仅建议在内网可信环境使用
- ❌ 数据无加密传输

## 与原版区别

### fake-komga-115 (forked from [xJogger/fake-komga-115](https://github.com/xJogger/fake-komga-115))

- 新增 `bangumi_series_meta` 表读取 → 元数据注入（标题/简介/标签/评分）
- 新增 `booksMetadata.authors` 字段 → Komikku 作者显示支持
- 新增 `booksMetadata.summary/summaryNumber/created/lastModified` 字段
- 新增 `SeriesMetadataDto.totalBookCount` 字段
- 阅读进度同步

### fake-komga-local

- 基于 fake-komga 框架，从零写的本地文件系统后端
- 支持多目录扫描
- 缩略图懒生成（从漫画第一页自动提取）
- 搜索 API（COLLATE NOCASE + 特殊字符转义）
- 标签/分类/出版社/作者过滤

### bangumi-metadata

- 完全独立的刮削器
- 支持手动匹配和批量自动刮削
- 可自定义正则提取搜索关键词
- 封面管理（生成/删除/回退）

## 技术栈

- Go 1.23+
- SQLite (modernc.org/sqlite, 纯 Go 无 CGO)
- Chi Router
- Docker + Alpine 镜像

## License

MIT