# 🚀 tenkai_server - GitラッパーAPIサーバー

tenkaiのバックエンドサーバー。Git操作とAI機能をREST APIとして提供します。

## 概要

tenkai_serverは、文筆家向けのGit操作を簡単にするためのAPIサーバーです。

### 主な機能

- **Git操作のラッパー**
  - 保存 (commit)
  - 履歴 (log)
  - 草案管理 (branch)
  - 校正依頼 (pull request)
  - 修正反映 (merge)

- **AI機能 (Gemini API)**
  - コミットメッセージ自動生成
  - 変更内容の要約
  - 文章校正・推敲支援

- **日本語API**
  - エラーメッセージの日本語化
  - 分かりやすいレスポンス

## 技術スタック

- Go (Gin framework)
- go-git (Git操作)
- Gemini API (AI機能)
- AI-First開発原則（1ファイル完結）

## API設計

```
POST   /api/save          - 原稿を保存 (commit)
GET    /api/history       - 履歴を取得 (log)
POST   /api/draft/create  - 草案を作成 (branch)
GET    /api/draft/list    - 草案一覧 (branch list)
POST   /api/draft/switch  - 草案切替 (checkout)
POST   /api/pr/create     - 校正依頼作成 (PR)
GET    /api/pr/list       - 校正依頼一覧
POST   /api/merge         - 修正反映 (merge)
```

## 開発方針

AI-First原則に従い、各エンドポイントは独立したファイルで実装します。