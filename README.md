# fake-komga

[![Go](https://img.shields.io/badge/Go-1.23%2B-blue)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-green)](https://github.com/tanlidoushen/fake-komga)

---

> 轻量级 Komga 兼容服务，Go 实现。  
> 对接 Mihon 系阅读器（Komikku / Tachiyomi 等）作为漫画后端。

---

## 背景

Komga 是一个优秀的漫画服务器，其数据库设计与 API 接口均较为成熟。  
但由于采用 Java 技术栈，在低配设备（如 NAS、开发板等）上内存占用偏高。

本项目使用 Go 语言实现了一套基础的 Komga 兼容 API，可对接 Mihon 系阅读器作为漫画后端。  
当前为测试版本，仅完成了基础功能适配。

---

## 项目结构

| 项目 | 说明 |
|------|------|
| [fake-komga-115](fake-komga-115/) | 以 115 网盘为存储后端，增加了元数据注入功能 |
| [fake-komga-local](fake-komga-local/) | 以本地文件系统为存储后端，直接扫描目录，支持完整 Komga API 兼容 |
| [bangumi-metadata](bangumi-metadata/) | 元数据刮削器，支持手动匹配 / 批量刮削 / 正则提取关键词 |
| [deploy](deploy/) | Docker Compose 部署配置 |

---

## 快速开始

```bash
git clone https://github.com/tanlidoushen/fake-komga.git
cd fake-komga/deploy
cp .env.example .env
# 编辑 .env 配置 BANGUMI_ACCESS_TOKEN 及漫画目录路径
docker compose up -d
```

| 服务 | 地址 |
|------|------|
| fake-komga-local API | `http://localhost:25604` |
| bangumi-metadata 管理页 | `http://localhost:25601` |
| Mihon / Komikku 添加服务器 | `http://<你的IP>:25604` |

---

## 配置

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `BANGUMI_ACCESS_TOKEN` | Bangumi API 令牌（刮削需要） | - |
| `COMICS_DIR` | 本地漫画目录路径 | `/comics` |
| `HTTP_PROXY` | 代理地址 | - |
| `TZ` | 时区 | `Asia/Shanghai` |

---

## ⚠️ 重要提示

当前为测试版本，存在以下限制：

- **无任何鉴权机制**（无登录、无 API Key、无 JWT）
- **不适用于公网部署**
- 仅建议在内网可信环境使用

---

## 鸣谢

- 感谢 [xJogger/fake-komga-115](https://github.com/xJogger/fake-komga-115) — 本仓库的两个 fake-komga 项目均基于此修改而来
- 感谢 [dyphire/KomgaBangumi](https://github.com/dyphire/KomgaBangumi) — bangumi-metadata 参考了此项目的刮削逻辑

---

> 遵循 [MIT License](https://github.com/tanlidoushen/fake-komga) 开源协议。