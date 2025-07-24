// AI-First原則：このファイルにすべての機能を実装
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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

// GitHub OAuth関連
type GitHubCallbackRequest struct {
	Code        string `json:"code" binding:"required"`
	RedirectURI string `json:"redirect_uri" binding:"required"`
}

type GitHubTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
}

type GitHubUser struct {
	ID        int    `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

// GitHub API関連の構造体
type GitHubRepository struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
	HTMLURL  string `json:"html_url"`
	CloneURL string `json:"clone_url"`
}

type GitHubFile struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	SHA         string `json:"sha"`
	Size        int    `json:"size"`
	URL         string `json:"url"`
	HTMLURL     string `json:"html_url"`
	GitURL      string `json:"git_url"`
	DownloadURL string `json:"download_url"`
	Type        string `json:"type"`
	Content     string `json:"content"`
	Encoding    string `json:"encoding"`
}

// Tenkai設定構造体
type TenkaiSettings struct {
	Version        string                 `json:"version"`
	CharsPerLine   int                    `json:"chars_per_line"`
	LinesPerPage   int                    `json:"lines_per_page"`
	WritingMode    string                 `json:"writing_mode"` // "vertical" or "horizontal"
	Theme          string                 `json:"theme"`        // "light" or "dark"
	Repositories   []string               `json:"repositories"` // リポジトリのフルネーム
	ActiveRepo     string                 `json:"active_repo"`
	CustomSettings map[string]interface{} `json:"custom_settings"`
	LastUpdated    string                 `json:"last_updated"`
}

// 設定取得/保存リクエスト
type SettingsRequest struct {
	AccessToken string         `json:"access_token" binding:"required"`
	Settings    TenkaiSettings `json:"settings,omitempty"`
}

// Gitラッパー用リクエスト構造体
type SouanTeishutsuRequest struct {
	AccessToken string `json:"access_token" binding:"required"`
	Repository  string `json:"repository" binding:"required"`
	Message     string `json:"message" binding:"required"`
	Branch      string `json:"branch"`
	Files       []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Mode    string `json:"mode"` // "100644" for regular files
	} `json:"files"`
}

type SouanRequest struct {
	AccessToken string `json:"access_token" binding:"required"`
	Repository  string `json:"repository" binding:"required"`
	Name        string `json:"name" binding:"required"`
	BaseBranch  string `json:"base_branch"` // デフォルト: main
}

type ShuseiIraiRequest struct {
	AccessToken string `json:"access_token" binding:"required"`
	Repository  string `json:"repository" binding:"required"`
	Branch      string `json:"branch" binding:"required"`
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
	BaseBranch  string `json:"base_branch"` // デフォルト: main
}

type KouseiIraiRequest struct {
	AccessToken string   `json:"access_token" binding:"required"`
	Repository  string   `json:"repository" binding:"required"`
	Branch      string   `json:"branch" binding:"required"`
	Title       string   `json:"title" binding:"required"`
	Description string   `json:"description"`
	Reviewers   []string `json:"reviewers"` // GitHubユーザー名のリスト
	BaseBranch  string   `json:"base_branch"` // デフォルト: main
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
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, Response{
			Success: true,
			Message: "tenkai API server is running",
		})
	})
	r.POST("/api/init", handleInit)
	r.POST("/api/save", handleSave)
	r.GET("/api/history", handleHistory)
	r.POST("/api/draft/create", handleDraftCreate)
	r.GET("/api/draft/list", handleDraftList)
	r.POST("/api/draft/switch", handleDraftSwitch)
	r.GET("/api/status", handleStatus)
	r.POST("/api/ai/analyze", handleAIAnalyze)
	r.GET("/api/auth/github/callback", handleGitHubCallback)
	// GitHub設定管理API
	r.GET("/api/settings", handleGetSettings)
	r.POST("/api/settings", handleSaveSettings)
	r.GET("/api/repositories", handleGetRepositories)
	// Gitラッパー API (日本語化対応)
	r.POST("/api/git/souan-teishutsu", handleSouanTeishutsu)    // 草案提出（commit）
	r.GET("/api/git/souan-list", handleSouanList)               // 草案一覧（branch list）
	r.POST("/api/git/souan-create", handleSouanCreate)          // 草案作成（branch create）
	r.POST("/api/git/souan-switch", handleSouanSwitch)          // 草案切替（branch switch）
	r.POST("/api/git/shusei-irai", handleShuseiIrai)            // 修正依頼（push & PR）
	r.POST("/api/git/kousei-irai", handleKouseiIrai)            // 校正依頼（push & PR with review）
	r.GET("/api/git/repository-info", handleRepositoryInfo)      // リポジトリ情報取得

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
	_, err = w.Add(".")
	if err != nil {
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

// GitHub OAuth認証処理
func handleGitHubCallback(c *gin.Context) {
	// GETパラメータから取得
	code := c.Query("code")
	// state := c.Query("state") // 現在は使用していないがCSRF対策で将来使用予定
	errorParam := c.Query("error")
	
	// フロントエンドURLを環境変数から取得
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "https://tenkai-production.up.railway.app"
	}
	
	// エラーチェック
	if errorParam != "" {
		c.Redirect(http.StatusTemporaryRedirect, fmt.Sprintf("%s/auth?error=%s", frontendURL, errorParam))
		return
	}
	
	if code == "" {
		c.Redirect(http.StatusTemporaryRedirect, fmt.Sprintf("%s/auth?error=missing_code", frontendURL))
		return
	}

	// GitHub OAuth App設定
	clientID := os.Getenv("GITHUB_CLIENT_ID")
	clientSecret := os.Getenv("GITHUB_CLIENT_SECRET")
	
	if clientID == "" || clientSecret == "" {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "OAuth設定が不足しています",
		})
		return
	}

	// アクセストークンを取得
	tokenURL := "https://github.com/login/oauth/access_token"
	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	data.Set("code", code)
	data.Set("redirect_uri", "https://tenkaiserver-production.up.railway.app/api/auth/github/callback")

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "リクエスト作成に失敗しました",
			Error:   err.Error(),
		})
		return
	}
	
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	
	client := &http.Client{}
	tokenResp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "GitHubトークン取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}
	defer tokenResp.Body.Close()

	tokenBody, err := io.ReadAll(tokenResp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "レスポンス読み取りに失敗しました",
			Error:   err.Error(),
		})
		return
	}

	// JSONレスポンスとしてパース
	var tokenResult GitHubTokenResponse
	if err := json.Unmarshal(tokenBody, &tokenResult); err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "トークンのパースに失敗しました",
			Error:   err.Error(),
		})
		return
	}

	accessToken := tokenResult.AccessToken
	if accessToken == "" {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "アクセストークンが取得できませんでした",
		})
		return
	}

	// ユーザー情報を取得
	userURL := "https://api.github.com/user"
	userReq, _ := http.NewRequest("GET", userURL, nil)
	userReq.Header.Set("Authorization", "Bearer "+accessToken)
	userReq.Header.Set("User-Agent", "tenkai-app")

	userResp, err := client.Do(userReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "ユーザー情報取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}
	defer userResp.Body.Close()

	var user GitHubUser
	if err := json.NewDecoder(userResp.Body).Decode(&user); err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "ユーザー情報のパースに失敗しました",
			Error:   err.Error(),
		})
		return
	}

	// 認証成功後、フロントエンドにリダイレクト（一時的な実装）
	redirectURL := fmt.Sprintf("%s/app?auth_success=true&token=%s&user=%s", 
		frontendURL,
		url.QueryEscape(accessToken), 
		url.QueryEscape(user.Login))
	
	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

// GitHub設定管理: 設定取得
func handleGetSettings(c *gin.Context) {
	accessToken := c.GetHeader("Authorization")
	if accessToken == "" {
		c.JSON(http.StatusUnauthorized, Response{
			Success: false,
			Message: "認証が必要です",
		})
		return
	}
	
	// "Bearer " プレフィックスを削除
	if strings.HasPrefix(accessToken, "Bearer ") {
		accessToken = strings.TrimPrefix(accessToken, "Bearer ")
	}

	// ユーザー情報を取得
	user, err := getGitHubUser(accessToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, Response{
			Success: false,
			Message: "GitHubユーザー情報の取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	// .tenkai-settings リポジトリから設定を取得
	settings, err := getTenkaiSettings(accessToken, user.Login)
	if err != nil {
		// 設定が存在しない場合はデフォルト設定を返す
		defaultSettings := TenkaiSettings{
			Version:        "1.0",
			CharsPerLine:   17,
			LinesPerPage:   42,
			WritingMode:    "vertical",
			Theme:          "light",
			Repositories:   []string{},
			ActiveRepo:     "",
			CustomSettings: make(map[string]interface{}),
			LastUpdated:    time.Now().Format(time.RFC3339),
		}
		
		c.JSON(http.StatusOK, Response{
			Success: true,
			Message: "デフォルト設定を返しました",
			Data:    defaultSettings,
		})
		return
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    settings,
	})
}

// GitHub設定管理: 設定保存
func handleSaveSettings(c *gin.Context) {
	var req SettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "リクエストが不正です",
			Error:   err.Error(),
		})
		return
	}

	// ユーザー情報を取得
	user, err := getGitHubUser(req.AccessToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, Response{
			Success: false,
			Message: "GitHubユーザー情報の取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	// 設定に最終更新日時を設定
	req.Settings.LastUpdated = time.Now().Format(time.RFC3339)

	// .tenkai-settings リポジトリに設定を保存
	err = saveTenkaiSettings(req.AccessToken, user.Login, req.Settings)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "設定の保存に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: "設定を保存しました",
		Data:    req.Settings,
	})
}

// GitHub設定管理: リポジトリ一覧取得
func handleGetRepositories(c *gin.Context) {
	accessToken := c.GetHeader("Authorization")
	if accessToken == "" {
		c.JSON(http.StatusUnauthorized, Response{
			Success: false,
			Message: "認証が必要です",
		})
		return
	}
	
	// "Bearer " プレフィックスを削除
	if strings.HasPrefix(accessToken, "Bearer ") {
		accessToken = strings.TrimPrefix(accessToken, "Bearer ")
	}

	// GitHubからリポジトリ一覧を取得
	repos, err := getGitHubRepositories(accessToken)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "リポジトリ一覧の取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    repos,
	})
}

// ヘルパー関数: GitHubユーザー情報取得
func getGitHubUser(accessToken string) (*GitHubUser, error) {
	userURL := "https://api.github.com/user"
	req, err := http.NewRequest("GET", userURL, nil)
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "tenkai-app")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API error: %d", resp.StatusCode)
	}

	var user GitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}

	return &user, nil
}

// ヘルパー関数: Tenkai設定取得
func getTenkaiSettings(accessToken, username string) (*TenkaiSettings, error) {
	// .tenkai-settings リポジトリから settings.json を取得
	fileURL := fmt.Sprintf("https://api.github.com/repos/%s/.tenkai-settings/contents/settings.json", username)
	req, err := http.NewRequest("GET", fileURL, nil)
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "tenkai-app")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("settings not found")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API error: %d", resp.StatusCode)
	}

	var file GitHubFile
	if err := json.NewDecoder(resp.Body).Decode(&file); err != nil {
		return nil, err
	}

	// Base64デコード
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return nil, err
	}

	var settings TenkaiSettings
	if err := json.Unmarshal(content, &settings); err != nil {
		return nil, err
	}

	return &settings, nil
}

// ヘルパー関数: Tenkai設定保存
func saveTenkaiSettings(accessToken, username string, settings TenkaiSettings) error {
	// 設定をJSONに変換
	settingsJSON, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	// Base64エンコード
	contentEncoded := base64.StdEncoding.EncodeToString(settingsJSON)

	// 既存ファイルのSHAを取得（更新の場合）
	existingSHA := ""
	existingSettings, err := getTenkaiSettings(accessToken, username)
	if err == nil && existingSettings != nil {
		// 既存ファイルがある場合、SHAを取得
		sha, _ := getFileSHA(accessToken, username, "settings.json")
		existingSHA = sha
	}

	// .tenkai-settings リポジトリが存在しない場合は作成
	err = ensureTenkaiSettingsRepo(accessToken, username)
	if err != nil {
		return err
	}

	// ファイルを更新/作成
	fileURL := fmt.Sprintf("https://api.github.com/repos/%s/.tenkai-settings/contents/settings.json", username)
	
	updateData := map[string]interface{}{
		"message": "tenkai設定を更新",
		"content": contentEncoded,
	}
	
	if existingSHA != "" {
		updateData["sha"] = existingSHA
	}

	updateJSON, err := json.Marshal(updateData)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", fileURL, strings.NewReader(string(updateJSON)))
	if err != nil {
		return err
	}
	
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "tenkai-app")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub API error: %d, %s", resp.StatusCode, string(body))
	}

	return nil
}

// ヘルパー関数: GitHubリポジトリ一覧取得
func getGitHubRepositories(accessToken string) ([]GitHubRepository, error) {
	reposURL := "https://api.github.com/user/repos?sort=updated&per_page=100"
	req, err := http.NewRequest("GET", reposURL, nil)
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "tenkai-app")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API error: %d", resp.StatusCode)
	}

	var repos []GitHubRepository
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, err
	}

	return repos, nil
}

// ヘルパー関数: .tenkai-settings リポジトリの確保
func ensureTenkaiSettingsRepo(accessToken, username string) error {
	// リポジトリが存在するかチェック
	repoURL := fmt.Sprintf("https://api.github.com/repos/%s/.tenkai-settings", username)
	req, err := http.NewRequest("GET", repoURL, nil)
	if err != nil {
		return err
	}
	
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "tenkai-app")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// リポジトリが既に存在する
		return nil
	}

	// リポジトリを作成
	createData := map[string]interface{}{
		"name":        ".tenkai-settings",
		"description": "tenkai editor settings",
		"private":     true,
		"auto_init":   true,
	}

	createJSON, err := json.Marshal(createData)
	if err != nil {
		return err
	}

	createURL := "https://api.github.com/user/repos"
	req, err = http.NewRequest("POST", createURL, strings.NewReader(string(createJSON)))
	if err != nil {
		return err
	}
	
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "tenkai-app")
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create repository: %d, %s", resp.StatusCode, string(body))
	}

	return nil
}

// ヘルパー関数: ファイルのSHA取得
func getFileSHA(accessToken, username, filename string) (string, error) {
	fileURL := fmt.Sprintf("https://api.github.com/repos/%s/.tenkai-settings/contents/%s", username, filename)
	req, err := http.NewRequest("GET", fileURL, nil)
	if err != nil {
		return "", err
	}
	
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "tenkai-app")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("file not found")
	}

	var file GitHubFile
	if err := json.NewDecoder(resp.Body).Decode(&file); err != nil {
		return "", err
	}

	return file.SHA, nil
}

// 草案提出（コミット）
func handleSouanTeishutsu(c *gin.Context) {
	var req SouanTeishutsuRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "リクエストが不正です",
			Error:   err.Error(),
		})
		return
	}

	// ブランチの設定（デフォルト: main）
	branch := req.Branch
	if branch == "" {
		branch = "main"
	}

	// 各ファイルをコミット
	for _, file := range req.Files {
		// ファイルの内容をBase64エンコード
		contentEncoded := base64.StdEncoding.EncodeToString([]byte(file.Content))
		
		// 既存ファイルのSHAを取得
		existingSHA := ""
		fileURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", req.Repository, file.Path)
		getReq, _ := http.NewRequest("GET", fileURL+"?ref="+branch, nil)
		getReq.Header.Set("Authorization", "Bearer "+req.AccessToken)
		getReq.Header.Set("User-Agent", "tenkai-app")
		
		client := &http.Client{}
		getResp, err := client.Do(getReq)
		if err == nil && getResp.StatusCode == http.StatusOK {
			defer getResp.Body.Close()
			var existingFile GitHubFile
			if json.NewDecoder(getResp.Body).Decode(&existingFile) == nil {
				existingSHA = existingFile.SHA
			}
		}
		
		// ファイルを更新/作成
		updateData := map[string]interface{}{
			"message": req.Message,
			"content": contentEncoded,
			"branch":  branch,
		}
		
		if existingSHA != "" {
			updateData["sha"] = existingSHA
		}
		
		updateJSON, _ := json.Marshal(updateData)
		
		putReq, _ := http.NewRequest("PUT", fileURL, strings.NewReader(string(updateJSON)))
		putReq.Header.Set("Authorization", "Bearer "+req.AccessToken)
		putReq.Header.Set("User-Agent", "tenkai-app")
		putReq.Header.Set("Content-Type", "application/json")
		
		putResp, err := client.Do(putReq)
		if err != nil {
			c.JSON(http.StatusInternalServerError, Response{
				Success: false,
				Message: "ファイルのコミットに失敗しました",
				Error:   err.Error(),
			})
			return
		}
		defer putResp.Body.Close()
		
		if putResp.StatusCode != http.StatusOK && putResp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(putResp.Body)
			c.JSON(http.StatusInternalServerError, Response{
				Success: false,
				Message: "ファイルのコミットに失敗しました",
				Error:   string(body),
			})
			return
		}
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: "草案を提出しました",
		Data: map[string]interface{}{
			"repository": req.Repository,
			"branch":     branch,
			"message":    req.Message,
		},
	})
}

// 草案一覧取得
func handleSouanList(c *gin.Context) {
	accessToken := c.GetHeader("Authorization")
	repository := c.Query("repository")
	
	if accessToken == "" || repository == "" {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "認証トークンとリポジトリ名が必要です",
		})
		return
	}
	
	// "Bearer " プレフィックスを削除
	if strings.HasPrefix(accessToken, "Bearer ") {
		accessToken = strings.TrimPrefix(accessToken, "Bearer ")
	}

	// ブランチ一覧を取得
	branchesURL := fmt.Sprintf("https://api.github.com/repos/%s/branches", repository)
	req, _ := http.NewRequest("GET", branchesURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "tenkai-app")
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "草案一覧の取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "草案一覧の取得に失敗しました",
			Error:   fmt.Sprintf("GitHub API error: %d", resp.StatusCode),
		})
		return
	}
	
	var branches []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&branches); err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "レスポンスのパースに失敗しました",
			Error:   err.Error(),
		})
		return
	}
	
	// 日本語化した草案情報を作成
	souanList := make([]map[string]interface{}, len(branches))
	for i, branch := range branches {
		souanList[i] = map[string]interface{}{
			"name":      branch["name"],
			"protected": branch["protected"],
			"commit":    branch["commit"],
		}
	}
	
	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    souanList,
	})
}

// 草案作成
func handleSouanCreate(c *gin.Context) {
	var req SouanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "リクエストが不正です",
			Error:   err.Error(),
		})
		return
	}

	// ベースブランチの設定（デフォルト: main）
	baseBranch := req.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	// ベースブランチの最新コミットを取得
	baseRefURL := fmt.Sprintf("https://api.github.com/repos/%s/git/refs/heads/%s", req.Repository, baseBranch)
	getReq, _ := http.NewRequest("GET", baseRefURL, nil)
	getReq.Header.Set("Authorization", "Bearer "+req.AccessToken)
	getReq.Header.Set("User-Agent", "tenkai-app")
	
	client := &http.Client{}
	getResp, err := client.Do(getReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "ベースブランチの取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}
	defer getResp.Body.Close()
	
	var baseRef map[string]interface{}
	if err := json.NewDecoder(getResp.Body).Decode(&baseRef); err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "ベースブランチ情報のパースに失敗しました",
			Error:   err.Error(),
		})
		return
	}
	
	// 新しいブランチの参照を作成
	createRefURL := fmt.Sprintf("https://api.github.com/repos/%s/git/refs", req.Repository)
	createData := map[string]interface{}{
		"ref": fmt.Sprintf("refs/heads/%s", req.Name),
		"sha": baseRef["object"].(map[string]interface{})["sha"],
	}
	
	createJSON, _ := json.Marshal(createData)
	
	postReq, _ := http.NewRequest("POST", createRefURL, strings.NewReader(string(createJSON)))
	postReq.Header.Set("Authorization", "Bearer "+req.AccessToken)
	postReq.Header.Set("User-Agent", "tenkai-app")
	postReq.Header.Set("Content-Type", "application/json")
	
	postResp, err := client.Do(postReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "草案の作成に失敗しました",
			Error:   err.Error(),
		})
		return
	}
	defer postResp.Body.Close()
	
	if postResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(postResp.Body)
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "草案の作成に失敗しました",
			Error:   string(body),
		})
		return
	}
	
	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: fmt.Sprintf("草案「%s」を作成しました", req.Name),
		Data: map[string]interface{}{
			"repository": req.Repository,
			"name":       req.Name,
			"baseBranch": baseBranch,
		},
	})
}

// 草案切替
func handleSouanSwitch(c *gin.Context) {
	// このAPIはクライアント側で現在の作業ブランチを管理するため、
	// サーバー側では特に処理は不要
	var req SouanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "リクエストが不正です",
			Error:   err.Error(),
		})
		return
	}
	
	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: fmt.Sprintf("草案「%s」に切り替えました", req.Name),
		Data: map[string]interface{}{
			"repository": req.Repository,
			"name":       req.Name,
		},
	})
}

// 修正依頼（プルリクエスト作成）
func handleShuseiIrai(c *gin.Context) {
	var req ShuseiIraiRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "リクエストが不正です",
			Error:   err.Error(),
		})
		return
	}

	// ベースブランチの設定（デフォルト: main）
	baseBranch := req.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	// プルリクエストを作成
	prURL := fmt.Sprintf("https://api.github.com/repos/%s/pulls", req.Repository)
	prData := map[string]interface{}{
		"title": req.Title,
		"body":  req.Description,
		"head":  req.Branch,
		"base":  baseBranch,
	}
	
	prJSON, _ := json.Marshal(prData)
	
	prReq, _ := http.NewRequest("POST", prURL, strings.NewReader(string(prJSON)))
	prReq.Header.Set("Authorization", "Bearer "+req.AccessToken)
	prReq.Header.Set("User-Agent", "tenkai-app")
	prReq.Header.Set("Content-Type", "application/json")
	
	client := &http.Client{}
	prResp, err := client.Do(prReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "修正依頼の作成に失敗しました",
			Error:   err.Error(),
		})
		return
	}
	defer prResp.Body.Close()
	
	if prResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(prResp.Body)
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "修正依頼の作成に失敗しました",
			Error:   string(body),
		})
		return
	}
	
	var prResult map[string]interface{}
	if err := json.NewDecoder(prResp.Body).Decode(&prResult); err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "レスポンスのパースに失敗しました",
			Error:   err.Error(),
		})
		return
	}
	
	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: "修正依頼を作成しました",
		Data: map[string]interface{}{
			"repository": req.Repository,
			"pullRequestNumber": prResult["number"],
			"pullRequestURL": prResult["html_url"],
		},
	})
}

// 校正依頼（レビュワー付きプルリクエスト作成）
func handleKouseiIrai(c *gin.Context) {
	var req KouseiIraiRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "リクエストが不正です",
			Error:   err.Error(),
		})
		return
	}

	// ベースブランチの設定（デフォルト: main）
	baseBranch := req.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	// プルリクエストを作成
	prURL := fmt.Sprintf("https://api.github.com/repos/%s/pulls", req.Repository)
	prData := map[string]interface{}{
		"title": req.Title,
		"body":  req.Description + "\n\n📝 校正をお願いします",
		"head":  req.Branch,
		"base":  baseBranch,
	}
	
	prJSON, _ := json.Marshal(prData)
	
	prReq, _ := http.NewRequest("POST", prURL, strings.NewReader(string(prJSON)))
	prReq.Header.Set("Authorization", "Bearer "+req.AccessToken)
	prReq.Header.Set("User-Agent", "tenkai-app")
	prReq.Header.Set("Content-Type", "application/json")
	
	client := &http.Client{}
	prResp, err := client.Do(prReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "校正依頼の作成に失敗しました",
			Error:   err.Error(),
		})
		return
	}
	defer prResp.Body.Close()
	
	if prResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(prResp.Body)
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "校正依頼の作成に失敗しました",
			Error:   string(body),
		})
		return
	}
	
	var prResult map[string]interface{}
	if err := json.NewDecoder(prResp.Body).Decode(&prResult); err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "レスポンスのパースに失敗しました",
			Error:   err.Error(),
		})
		return
	}
	
	// レビュワーを追加
	if len(req.Reviewers) > 0 {
		reviewURL := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%v/requested_reviewers", 
			req.Repository, prResult["number"])
		reviewData := map[string]interface{}{
			"reviewers": req.Reviewers,
		}
		
		reviewJSON, _ := json.Marshal(reviewData)
		
		reviewReq, _ := http.NewRequest("POST", reviewURL, strings.NewReader(string(reviewJSON)))
		reviewReq.Header.Set("Authorization", "Bearer "+req.AccessToken)
		reviewReq.Header.Set("User-Agent", "tenkai-app")
		reviewReq.Header.Set("Content-Type", "application/json")
		
		reviewResp, err := client.Do(reviewReq)
		if err != nil {
			// レビュワー追加に失敗してもPRは作成されているので、警告のみ
			log.Printf("レビュワーの追加に失敗: %v", err)
		} else {
			reviewResp.Body.Close()
		}
	}
	
	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: "校正依頼を作成しました",
		Data: map[string]interface{}{
			"repository": req.Repository,
			"pullRequestNumber": prResult["number"],
			"pullRequestURL": prResult["html_url"],
			"reviewers": req.Reviewers,
		},
	})
}

// リポジトリ情報取得
func handleRepositoryInfo(c *gin.Context) {
	accessToken := c.GetHeader("Authorization")
	repository := c.Query("repository")
	
	if accessToken == "" || repository == "" {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Message: "認証トークンとリポジトリ名が必要です",
		})
		return
	}
	
	// "Bearer " プレフィックスを削除
	if strings.HasPrefix(accessToken, "Bearer ") {
		accessToken = strings.TrimPrefix(accessToken, "Bearer ")
	}

	// リポジトリ情報を取得
	repoURL := fmt.Sprintf("https://api.github.com/repos/%s", repository)
	req, _ := http.NewRequest("GET", repoURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "tenkai-app")
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "リポジトリ情報の取得に失敗しました",
			Error:   err.Error(),
		})
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "リポジトリ情報の取得に失敗しました",
			Error:   fmt.Sprintf("GitHub API error: %d", resp.StatusCode),
		})
		return
	}
	
	var repoInfo map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&repoInfo); err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Message: "レスポンスのパースに失敗しました",
			Error:   err.Error(),
		})
		return
	}
	
	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    repoInfo,
	})
}

