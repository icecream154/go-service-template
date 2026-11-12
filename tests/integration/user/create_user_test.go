//go:build integration

package user_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go-service-template/tests/integration"
)

// TestCreateUser 验证新外部账户的匿名注册。向真实 HTTP 入口提交资料后，
// 检查成功响应和签发的凭证，再用独立的数据库连接核对初始用户资料。
func TestCreateUser(t *testing.T) {
	// 调用方先构建带 integration_app 标签的本机二进制。缺少路径时在创建资源前报告用法。
	executable := os.Getenv("INTEGRATION_WEB_BINARY")
	if executable == "" {
		t.Fatal("set INTEGRATION_WEB_BINARY to the absolute path of cmd/web built with -tags=integration_app")
	}

	// 应用启动需要装配公司服务 Client。注册流程只需数据库、发号器和 JWT，本机桩收到意外请求时会让用例失败。
	company := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("create user made an unexpected company service request (%s)", r.Method)
		http.Error(w, "unexpected company service request", http.StatusBadGateway)
	}))
	// Cleanup 按逆序执行。先登记模拟服务，再让 StartApplication 登记应用和 MySQL 的清理。
	t.Cleanup(company.Close)

	// 使用仓库正式 SQL 建立独立 MySQL，准备本轮配置并启动调用方提供的服务进程。
	// 这里返回时应用已经通过 HTTP 就绪检查，可以开始发业务请求。
	app := integration.StartApplication(t, integration.ApplicationOptions{
		Executable: executable,
		PrepareConfig: func(ctx context.Context, configDir string) error {
			return prepareTestConfig(ctx, configDir, company.URL)
		},
	})

	// 独立环境中该账户尚未注册。外部标识使用带前导零的字符串，昵称作为明确的业务预期。
	const (
		accountSource     = "integration"
		externalAccountID = "0007"
		userName          = "Integration User"
	)
	// 以 JSON 请求体走真实参数绑定，省略可选头像字段，验证首次注册的默认资料。
	requestBody, err := json.Marshal(map[string]string{
		"account_source":      accountSource,
		"external_account_id": externalAccountID,
		"user_name":           userName,
	})
	if err != nil {
		t.Fatalf("encode create user request: %v", err)
	}
	// 首次注册允许匿名调用，使用已就绪服务的地址构造实际 POST 请求。
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		app.BaseURL+"/api/v1/user/userProfile/createUser", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatalf("build create user request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	// 直接连接本轮的回环地址。单次请求有独立时限，重定向响应交给下面的状态码断言处理。
	client := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		Timeout:   15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send create user request (service log %s): %v", app.ProcessLogPath, err)
	}
	// 在用例返回前释放响应连接，随后由 Cleanup 收尾服务和依赖。
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// 响应可能携带签发的 JWT，失败时只输出状态码和诊断文件位置。
		t.Fatalf("create user HTTP status = %d, want %d (service log %s)", response.StatusCode, http.StatusOK, app.ProcessLogPath)
	}

	// 按 HTTP 契约解码需要断言的字段。user_id 用 string 检查线上的 JSON 类型，version 用指针区分缺失与零。
	var result struct {
		Data *struct {
			UserProfile struct {
				UserID    string          `json:"user_id"`
				UserName  string          `json:"user_name"`
				AvatarURL json.RawMessage `json:"avatar_url"`
				IsEnabled bool            `json:"is_enabled"`
				Version   *int64          `json:"version"`
			} `json:"user_profile"`
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
		} `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	// 限制读取响应的大小，解码失败时用进程日志定位问题。
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		t.Fatalf("decode create user response (service log %s): %v", app.ProcessLogPath, err)
	}
	if result.Data == nil || string(result.Error) != "null" {
		t.Fatal("create user response must contain data and a null error")
	}

	// 核对服务生成的内部 ID，以及新用户应具有的昵称、空头像、启用状态和初始版本。
	profile := result.Data.UserProfile
	userID, err := strconv.ParseInt(profile.UserID, 10, 64)
	if err != nil || userID <= 0 {
		t.Fatal("create user response must contain a positive decimal user_id string")
	}
	if profile.UserName != userName || string(profile.AvatarURL) != "null" || !profile.IsEnabled || profile.Version == nil || *profile.Version != 0 {
		t.Fatal("created user profile must contain the requested name, a null avatar, enabled status, and version zero")
	}
	// 注册成功会签发 Bearer 凭证。断言其存在，诊断中不输出凭证值。
	if result.Data.AccessToken == "" || result.Data.TokenType != "Bearer" {
		t.Fatal("create user response must contain a Bearer access token")
	}

	// 通过 testkit 持有的 user 连接池读取已提交的资料。用请求中的外部账户定位行，再和 HTTP 返回的内部 ID 对照。
	db, err := app.Database.DB("user")
	if err != nil {
		t.Fatalf("get user database connection: %v", err)
	}
	// 查询使用独立时限。连接池由 StartApplication 在用例结束时统一关闭。
	queryCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var storedID, storedVersion int64
	var storedName string
	var storedEnabled bool
	if err := db.QueryRowContext(queryCtx, `
SELECT id, user_name, is_enabled, version
FROM user_service.user_profile
WHERE account_source = ? AND external_account_id = ?`, accountSource, externalAccountID).
		Scan(&storedID, &storedName, &storedEnabled, &storedVersion); err != nil {
		t.Fatalf("query committed user profile: %v", err)
	}
	if storedID != userID || storedName != userName || !storedEnabled || storedVersion != 0 {
		t.Errorf("stored profile = {id:%d name:%q enabled:%t version:%d}, want {id:%d name:%q enabled:true version:0}",
			storedID, storedName, storedEnabled, storedVersion, userID, userName)
	}
}

// prepareTestConfig 为本轮应用生成启动所需的四份 test.yml。
// configDir 由 StartApplication 传入，已包含本轮 MySQL 配置。companyURL 来自用例的回环模拟服务。
// ctx 是用例上下文。配置准备失败时返回阶段和路径，启动器负责清理数据库。
func prepareTestConfig(ctx context.Context, configDir, companyURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// JWT 加载器要求标准 Base64，且 HS256 密钥解码后至少有 32 字节。每轮用例生成独立密钥。
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return fmt.Errorf("generate integration JWT key: %w", err)
	}
	signingKey := base64.StdEncoding.EncodeToString(key[:])

	// 配置值仅来自本轮用例和模拟服务。进程日志由启动器捕获，数据库配置由 testkit 提供。
	files := []struct {
		path     string
		contents string
	}{
		// 日志加载器要求大写级别，省略 file_path 使日志走启动器捕获的标准输出。
		{"logger/test.yml", "service_name: user-integration\nmin_level: INFO\n"},
		// 签发方、接收方和有效期使用明确的测试值，签名密钥直接写入私有文件。
		{"authn/jwt/test.yml", fmt.Sprintf(`issuer: user-integration
audience: user-integration-client
signing_key: %q
token_ttl: "5m"
`, signingKey)},
		// 本轮服务有独立实例与数据库，固定的机器号足以产生本用例需要的内部用户 ID。
		{"snowflake/test.yml", "machine_id: 1\n"},
		// 两个 image Client 都是启动时的配置要求，目标统一使用同一个本机桩。
		{"integrations/company/test.yml", fmt.Sprintf(`python-service-template:
  base_url: %q
  connect_timeout: 1s
  image:
    image_edit:
      request_timeout: 1s
    image_gen:
      request_timeout: 1s
`, companyURL)},
	}

	// 在临时配置根目录下逐个写入，限制新建目录和文件的访问权限。
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(configDir, filepath.FromSlash(file.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create integration config directory for %s: %w", file.path, err)
		}
		if err := os.WriteFile(path, []byte(file.contents), 0o600); err != nil {
			return fmt.Errorf("write integration config %s: %w", file.path, err)
		}
	}
	return nil
}
