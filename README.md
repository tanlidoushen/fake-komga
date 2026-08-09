# fake-komga

轻量级 Komga 兼容服务，Go 实现，低资源占用。为 Mihon/Komikku/Tachiyomi 系软件提供漫画阅读服务。

## 项目结构

```
fake-komga/
├── fake-komga-115/         # 115 网盘后端
│                            # forked from https://github.com/xJogger/fake-komga-115
│                            # 增加了标签、作者等字段的推送
├── fake-komga-local/       # 本地文件系统为存储后端，直接扫描目录
│                            # 同上的完整 Komga API 兼容
├── bangumi-metadata/       # Bangumi 元数据刮削器
│                            # 手动匹配/批量刮削/正则提取关键词
│                            # 写入同一数据库，fake-komga 自动读取注入
└── deploy/                 # 部署配置
    ├── docker-compose.yml
    └── .env.example
```

> ⚠️ 以下是测试阶段使用的方案，实际部署根据需要选择一种后端即可。

## 背景

Komga 的内存占用纯是 Java 整出来的，本身数据库和 API 设计没啥大问题。
现在只是测试版本，只有一个基础的对 Mihon 系的适配。

## 功能

### fake-komga-115
- 以 115 网盘为存储后端，通过 115 Open API 读取漫画
- 完整 Komga API 兼容（series/books/pages/thumbnail/read-progress）
- 搜索、分页、排序、过滤
- 元数据注入（从 bangumi_series_meta 表读取）

### fake-komga-local
- 本地文件系统为存储后端，直接扫描目录
- 同上的完整 Komga API 兼容

### bangumi-metadata
- Bangumi 元数据手动匹配（搜索→确认）
- 批量自动刮削（全部/已选系列）
- 正则提取关键词（可自定义提取规则）
- 系列详情编辑（标题/简介/标签/作者/封面/出版社）
- 封面管理（生成/删除缩略图）

## 快速开始

### 前置要求

- Docker & Docker Compose

### 本地模式

```bash
git clone https://github.com/tanlidoushen/fake-komga.git
cd fake-komga/deploy
cp .env.example .env
# 编辑 .env，设置 BANGUMI_ACCESS_TOKEN
# 编辑 docker-compose.yml，修改 COMICS_DIR 为漫画目录路径
docker compose up -d
```

### 访问地址

| 服务 | 地址 |
|------|------|
| fake-komga-local API | `http://localhost:25604` |
| bangumi-metadata 管理页 | `http://localhost:25601` |
| Mihon/Komikku 添加服务器 | `http://<你的IP>:25604` |

## 配置

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `BANGUMI_ACCESS_TOKEN` | Bangumi API 令牌（刮削需要） | - |
| `COMICS_DIR` | 本地漫画目录路径 | `/comics` |
| `HTTP_PROXY` | 代理地址 | - |
| `TZ` | 时区 | `Asia/Shanghai` |

## ⚠️ 重要提示

**当前为测试版本，存在以下限制：**

- ❌ **无任何鉴权机制**（无登录、无 API Key、无 JWT）
- ❌ **不适用于公网部署**
- ❌ 仅建议在内网可信环境使用

## 技术栈

- Go 1.23+
- SQLite (modernc.org/sqlite, 纯 Go 无 CGO)
- Chi Router
- Docker + Alpine 镜像

## License

MIT