// AI-First原則：このファイルにすべての機能を実装
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// グローバル変数
var (
	repo      *git.Repository
	workDir   string
	genClient *genai.Client
	model     *genai.GenerativeModel
)

// レスポンス型
type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// 初期化リクエスト
type InitRequest struct {
	WorkDir string `json:"workDir" binding:"required"`
}

// 保存リクエスト
type SaveRequest struct {
	Message string `json:"message"`
	UseAI   bool   `json:"useAI"`
}

// 草案作成リクエスト
type DraftRequest struct {
	Name string `json:"name" binding:"required"`
}

// AI分析リクエスト
type AnalyzeRequest struct {
	Text   string `json:"text" binding:"required"`
	Type   string `json:"type"` // "summary", "review", "commit"
	Prompt string `json:"prompt"`
}

func main() {
	// 環境変数からGemini APIキーを取得して初期化
	geminiAPIKey := os.Getenv("GEMINI_API_KEY")
	if geminiAPIKey != "" {
		ctx := context.Background()
		var err error
		genClient, err = genai.NewClient(ctx, option.WithAPIKey(geminiAPIKey))
		if err != nil {
			log.Printf("Gemini API初期化エラー: %v", err)
		} else {
			model = genClient.GenerativeModel("gemini-pro")
			model.SetTemperature(0.7)
			log.Println("Gemini APIを環境変数から初期化しました")
		}
	} else {
		log.Println("GEMINI_API_KEY環境変数が設定されていません")
	}

	// Ginの初期化
	r := gin.Default()

	// CORS設定
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		
		c.Next()
	})

	// ルート定義
	r.POST("/api/init", handleInit)
	r.POST("/api/save", handleSave)
	r.GET("/api/history", handleHistory)
	r.POST("/api/draft/create", handleDraftCreate)
	r.GET("/api/draft/list", handleDraftList)
	r.POST("/api/draft/switch", handleDraftSwitch)
	r.GET("/api/status", handleStatus)
	r.POST("/api/ai/analyze", handleAIAnalyze)

	// サーバー起動
	port := os.Getenv("PORT")
	if port == "" {
		port = "3001"
	}

	log.Printf("tenkai_server が起動しました: http://localhost:%s", port)
	log.Fatal(r.Run(":" + port))
}

// 初期化
func handleInit(c *gin.Context) {
	var req InitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "リクエストが不正です",
			Error:   err.Error(),
		})
		return
	}

	workDir = req.WorkDir

	// Gitリポジトリを開く/初期化
	var err error
	repo, err = git.PlainOpen(workDir)
	if err != nil {
		// リポジトリが存在しない場合は初期化
		repo, err = git.PlainInit(workDir, false)
		if err != nil {
			c.JSON(http.StatusInternalServerError, Response{
				Success: false,
				Message: "Gitリポジトリの初期化に失敗しました",
				Error:   err.Error(),
			})
			return
		}
	}


	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: "原稿管理を開始しました",
		Data: map[string]string{
			"workDir": workDir,
			"aiEnabled": fmt.Sprintf("%v", genClient != nil),
		},
	})
}

// 保存 (commit)
func handleSave(c *gin.Context) {
	var req SaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "リクエストが不正です",
			Error:   err.Error(),
		})
		return
	}

	if repo == nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "先に初期化してください",
		})
		return
	}

	// ワークツリーを取得
	w, err := repo.Worktree()
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "作業ツリーの取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	// すべての変更をステージング
	if err := w.Add("."); err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "変更のステージングに失敗しました",
			Error:   err.Error(),
		})
		return
	}

	// コミットメッセージの生成
	commitMessage := req.Message
	if commitMessage == "" {
		commitMessage = fmt.Sprintf("%s - 自動保存", time.Now().Format("2006/01/02 15:04:05"))
	}

	// AIによるコミットメッセージ生成
	if req.UseAI && genClient != nil {
		status, _ := w.Status()
		changes := formatChanges(status)
		if changes != "" {
			aiMessage := generateAICommitMessage(changes)
			if aiMessage != "" {
				commitMessage = aiMessage
			}
		}
	}

	// コミット
	commit, err := w.Commit(commitMessage, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "tenkai",
			Email: "tenkai@example.com",
			When:  time.Now(),
		},
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "保存に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: "保存しました",
		Data: map[string]string{
			"commit": commit.String()[:7],
			"message": commitMessage,
		},
	})
}

// 履歴取得
func handleHistory(c *gin.Context) {
	if repo == nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "先に初期化してください",
		})
		return
	}

	// コミット履歴を取得
	ref, err := repo.Head()
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "履歴の取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	cIter, err := repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "履歴の取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	var history []map[string]string
	limit := 20
	count := 0

	err = cIter.ForEach(func(commit *object.Commit) error {
		if count >= limit {
			return nil
		}
		history = append(history, map[string]string{
			"id":      commit.Hash.String()[:7],
			"date":    commit.Author.When.Format("2006/01/02 15:04:05"),
			"message": commit.Message,
			"author":  commit.Author.Name,
		})
		count++
		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "履歴の読み込みに失敗しました",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    history,
	})
}

// 草案作成
func handleDraftCreate(c *gin.Context) {
	var req DraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "リクエストが不正です",
			Error:   err.Error(),
		})
		return
	}

	if repo == nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "先に初期化してください",
		})
		return
	}

	// ワークツリーを取得
	w, err := repo.Worktree()
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "作業ツリーの取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	// 新しいブランチを作成してチェックアウト
	branchName := plumbing.NewBranchReferenceName(req.Name)
	err = w.Checkout(&git.CheckoutOptions{
		Create: true,
		Branch: branchName,
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "草案の作成に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: fmt.Sprintf("草案「%s」を作成しました", req.Name),
		Data: map[string]string{
			"draft": req.Name,
		},
	})
}

// 草案一覧
func handleDraftList(c *gin.Context) {
	if repo == nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "先に初期化してください",
		})
		return
	}

	// 現在のブランチを取得
	head, err := repo.Head()
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "現在の草案の取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}
	currentBranch := head.Name().Short()

	// ブランチ一覧を取得
	branches, err := repo.Branches()
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "草案一覧の取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	var drafts []map[string]interface{}
	err = branches.ForEach(func(ref *plumbing.Reference) error {
		drafts = append(drafts, map[string]interface{}{
			"name":    ref.Name().Short(),
			"current": ref.Name().Short() == currentBranch,
		})
		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "草案一覧の読み込みに失敗しました",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    drafts,
	})
}

// 草案切替
func handleDraftSwitch(c *gin.Context) {
	var req DraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "リクエストが不正です",
			Error:   err.Error(),
		})
		return
	}

	if repo == nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "先に初期化してください",
		})
		return
	}

	// ワークツリーを取得
	w, err := repo.Worktree()
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "作業ツリーの取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	// ブランチにチェックアウト
	branchName := plumbing.NewBranchReferenceName(req.Name)
	err = w.Checkout(&git.CheckoutOptions{
		Branch: branchName,
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "草案の切り替えに失敗しました",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: fmt.Sprintf("草案「%s」に切り替えました", req.Name),
		Data: map[string]string{
			"draft": req.Name,
		},
	})
}

// 状態確認
func handleStatus(c *gin.Context) {
	if repo == nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "先に初期化してください",
		})
		return
	}

	// ワークツリーを取得
	w, err := repo.Worktree()
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "作業ツリーの取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	// 状態を取得
	status, err := w.Status()
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "状態の取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	// 現在のブランチを取得
	head, err := repo.Head()
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "現在の草案の取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	modifiedCount := 0
	for _, s := range status {
		if s.Staging != git.Unmodified || s.Worktree != git.Unmodified {
			modifiedCount++
		}
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data: map[string]interface{}{
			"current":     head.Name().Short(),
			"modified":    modifiedCount,
			"hasChanges":  !status.IsClean(),
		},
	})
}

// AI分析
func handleAIAnalyze(c *gin.Context) {
	var req AnalyzeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "リクエストが不正です",
			Error:   err.Error(),
		})
		return
	}

	if genClient == nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "AI機能が初期化されていません",
		})
		return
	}

	prompt := ""
	switch req.Type {
	case "summary":
		prompt = "以下の文章を簡潔に要約してください：\n\n" + req.Text
	case "review":
		prompt = "以下の文章を校正し、改善点を指摘してください：\n\n" + req.Text
	case "commit":
		prompt = "以下の変更内容から適切な日本語のコミットメッセージを生成してください：\n\n" + req.Text
	default:
		prompt = req.Prompt
		if prompt == "" {
			prompt = req.Text
		}
	}

	ctx := context.Background()
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "AI分析に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	var result string
	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				result += fmt.Sprintf("%v", part)
			}
		}
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data: map[string]string{
			"result": result,
			"type":   req.Type,
		},
	})
}

// ヘルパー関数：変更内容のフォーマット
func formatChanges(status git.Status) string {
	var changes []string
	for file, s := range status {
		if s.Staging != git.Unmodified {
			changes = append(changes, fmt.Sprintf("%s: %s", file, s.Staging))
		} else if s.Worktree != git.Unmodified {
			changes = append(changes, fmt.Sprintf("%s: %s", file, s.Worktree))
		}
	}
	return strings.Join(changes, "\n")
}

// ヘルパー関数：AIによるコミットメッセージ生成
func generateAICommitMessage(changes string) string {
	if genClient == nil {
		return ""
	}

	ctx := context.Background()
	prompt := fmt.Sprintf("以下の変更内容から、簡潔で分かりやすい日本語のコミットメッセージを1行で生成してください。技術的な詳細は避け、何をしたかを明確に：\n\n%s", changes)
	
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		log.Printf("AI生成エラー: %v", err)
		return ""
	}

	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				return strings.TrimSpace(fmt.Sprintf("%v", part))
			}
		}
	}
	
	return ""
}