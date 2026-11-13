//go:build business

// 产品流程业务测试：新用户注册、AI 头像生成与个人资料设置闭环
// 主属业务域      ：user（协同依赖：第三方 Qwen LLM SDK + 公司内部 python-service-template 图片生成服务）
// 涉及数据库表    ：user_service.user_profile
// 串联 HTTP 接口  ：
//   1. POST /api/v1/user/userProfile/createUser
//   2. GET  /api/v1/user/userProfile/queryUserProfileByUserId
//   3. POST /api/v1/user/userProfile/generateUserAvatar
//   4. POST /api/v1/user/userProfile/updateUserProfile
//   5. GET  /api/v1/user/userProfile/queryUserProfilePage
//   6. POST /api/v1/user/userProfile/batchQueryUserProfiles
//
// 产品用例与变体总览（共 5 个流程场景）：
//   01_register_generate_and_apply_avatar                        [主流程]     注册取得真实凭证 -> 查初始资料 -> 生成 AI 头像（资料零变更）-> 乐观锁更新 -> 本人/后台/DB 三方终态一致
//   02_preserve_registered_account_when_avatar_generation_fails  [故障分支]   注册已提交后 Qwen SDK 故障返回 500，阻断图片服务调用，已创建账户保留初始态且仍可正常登录查询
//   03_reject_stale_version_replay_and_keep_latest_profile       [冲突分支]   首次更新成功后再次生成头像并故意重放旧版本号返回 409，线上资料保持上次成功提交值不被覆盖
//   04_forbid_applying_generated_avatar_to_other_user            [权限分支]   生成头像后持本人凭证试图修改存量他人资料返回 403，本人与他人的资料均保持各自合法状态
//   05_isolate_company_image_timeout_without_polluting_next_flow [超时隔离]   使用 Isolated 独占环境模拟公司图片服务慢响应超时，核对已注册账户未受污染并安全排空销毁进程
package user_business_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"go-service-template/tests/business/domains/userdom"
	"go-service-template/tests/harness"
	"go-service-template/tests/testkit/apicall"
	"go-service-template/tests/testkit/dbassert"
	"go-service-template/tests/testkit/flow"
	"go-service-template/tests/testkit/httpstub"
	"go-service-template/tests/testkit/sdkmock"
	"go-service-template/tests/testkit/seed"
)

func TestRegisterAndSetGeneratedAvatar(t *testing.T) {
	t.Run("01_register_generate_and_apply_avatar", func(t *testing.T) {
		// [产品目标]
		// 新外部账户首次注册后，全程仅凭服务端真实签发的 Bearer Token 与返回的真实雪花 ID，
		// 依次完成初始资料核验、AI 头像预览生成（期间不修改数据库）、乐观锁资料更新，
		// 最终从本人接口、后台管理分页接口及独立数据库连接三个视角确认状态一致，且存量用户不受影响。
		env := suite.Shared(t)
		sd := seed.New(t, env.DB)
		fl := harness.NewFlow(t, env, flow.WithSeed(sd))

		// ==================== [1. 前置存量状态（Given State）] ====================
		userdom.InsertProfile(sd, userdom.ProfileSeed{
			ID:                sd.ID("user:bystander"),
			AccountSource:     "wechat",
			ExternalAccountID: "wx_existing_bystander_01",
			UserName:          "存量路人甲",
			AvatarURL:         sql.NullString{String: "https://cdn.example.com/avatars/bystander.png", Valid: true},
			IsEnabled:         true,
			Version:           2,
			CreatedAt:         sd.Time(-48 * time.Hour),
		})
		sd.Freeze()

		bystanderSnap := dbassert.CaptureSingleRow(t, env.DB(t, "user"),
			"SELECT user_name, avatar_url, is_enabled, version FROM user_profile WHERE id = ?",
			sd.ID("user:bystander"),
		)

		// ==================== [2. 跨步骤流转变量（Flow Handoff State）] ====================
		var (
			createdUserID  int64
			issuedToken    string
			initialVersion int64
			generatedURL   string
			updatedVersion int64
		)

		// ==================== [3. 有序产品步骤推进（Ordered Steps）] ====================
		fl.Step("01_register_new_user_account", func(s *flow.Step) {
			resp := userdom.CallCreateUser(s, userdom.CreateUserRequest{
				AccountSource:     "wechat",
				ExternalAccountID: "wx_new_member_20261113",
				UserName:          "初来乍到的小明",
			})
			if gotCache := resp.Header.Get("Cache-Control"); gotCache != "no-store" {
				s.T().Fatalf("createUser Cache-Control = %q, want no-store", gotCache)
			}
			created := apicall.RequireSuccess[userdom.CreateUserData](s.T(), resp)
			if created.TokenType != "Bearer" || strings.TrimSpace(created.AccessToken) == "" {
				s.T().Fatalf("expected non-empty Bearer token, got token_type=%q", created.TokenType)
			}
			if created.UserProfile.Version != 0 || created.UserProfile.AvatarURL != nil || !created.UserProfile.IsEnabled {
				s.T().Fatalf("unexpected initial profile in createUser response: %+v", created.UserProfile)
			}

			createdUserID = userdom.MustParseID(s.T(), created.UserProfile.UserID)
			issuedToken = created.AccessToken
		})

		fl.Step("02_query_initial_profile_using_issued_token", func(s *flow.Step) {
			initial := userdom.MustQueryProfileByUserID(s, createdUserID, apicall.WithBearerToken(issuedToken))
			if initial.UserName != "初来乍到的小明" || initial.AvatarURL != nil || initial.Version != 0 {
				s.T().Fatalf("unexpected queried initial profile: %+v", initial)
			}
			initialVersion = initial.Version
		})

		fl.Step("03_generate_avatar_via_qwen_and_image_service", func(s *flow.Step) {
			selfBeforeGen := dbassert.CaptureSingleRow(s.T(), env.DB(s.T(), "user"),
				"SELECT user_name, avatar_url, version, updated_at FROM user_profile WHERE id = ?",
				createdUserID,
			)

			const rawPrompt = "戴飞行员护目镜的柴犬"
			const optimizedPrompt = "clean flat vector portrait of a shiba inu wearing aviator goggles, centered avatar"
			const expectedImageURL = "https://cdn.example.com/generated/shiba-aviator-001.png"

			qwenRule := s.ExpectSDK("ai.llm.qwen", "GenerateText", sdkmock.RespondJSON(map[string]any{
				"content": optimizedPrompt,
			}))
			imgRoute := s.ExpectHTTP("python-service-template",
				http.MethodPost, "/api/v1/image/imageGen/generateImage",
				httpstub.JSONResponse(http.StatusOK, map[string]any{
					"image_url": expectedImageURL,
					"width":     512,
					"height":    512,
				}),
			)

			gen := userdom.MustGenerateAvatar(s, rawPrompt, apicall.WithBearerToken(issuedToken))
			if gen.AvatarURL != expectedImageURL {
				s.T().Fatalf("generated avatar_url = %q, want %q", gen.AvatarURL, expectedImageURL)
			}
			generatedURL = gen.AvatarURL

			// 核对 Qwen 收到原始描述，且内部图片生成服务收到的是 Qwen 优化后的英文提示词。
			qwenCalls := qwenRule.RequireCallCount(s.T(), 1)
			if !strings.Contains(string(qwenCalls[0].ParamsJSON), rawPrompt) {
				s.T().Fatalf("Qwen SDK payload did not contain raw prompt %q", rawPrompt)
			}
			imgCalls := imgRoute.RequireCallCount(s.T(), 1)
			var imgReq struct {
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal(imgCalls[0].Body, &imgReq); err != nil || imgReq.Prompt != optimizedPrompt {
				s.T().Fatalf("company image service prompt = %q (err=%v), want %q", imgReq.Prompt, err, optimizedPrompt)
			}

			// 核对禁止副作用：仅生成头像预览绝不修改数据库中的用户资料或版本号。
			selfBeforeGen.RequireUnchanged(s.T())
		})

		fl.Step("04_update_own_profile_with_generated_avatar", func(s *flow.Step) {
			updated := userdom.MustUpdateProfile(s, userdom.UpdateProfileRequest{
				UserID:          createdUserID,
				ExpectedVersion: initialVersion,
				UserName:        "飞行员小明",
				AvatarURL:       &generatedURL,
			}, apicall.WithBearerToken(issuedToken))

			if updated.UserName != "飞行员小明" || updated.AvatarURL == nil || *updated.AvatarURL != generatedURL {
				s.T().Fatalf("unexpected updated profile fields: %+v", updated)
			}
			if updated.Version != initialVersion+1 {
				s.T().Fatalf("updated version = %d, want %d", updated.Version, initialVersion+1)
			}
			updatedVersion = updated.Version
		})

		// ==================== [4. 最终产品可见态与跨库持久化核对（End-State Audit）] ====================
		fl.Step("05_verify_end_state_across_member_admin_and_database", func(s *flow.Step) {
			// 视角 A：用户本人通过查询接口读取最终资料。
			selfView := userdom.MustQueryProfileByUserID(s, createdUserID, apicall.WithBearerToken(issuedToken))
			if selfView.UserName != "飞行员小明" || selfView.AvatarURL == nil || *selfView.AvatarURL != generatedURL || selfView.Version != updatedVersion {
				s.T().Fatalf("member query view mismatch: %+v", selfView)
			}

			// 视角 B：后台运营服务通过分页接口按精确新昵称检索。
			adminToken := env.Auth.IssueServiceAdminToken(s.T(), "ops-console")
			adminPage := userdom.MustQueryProfilePage(s, userdom.QueryProfilePageRequest{
				UserName:   "飞行员小明",
				PageNumber: 1,
				PageSize:   10,
			}, apicall.WithBearerToken(adminToken))
			if adminPage.TotalCount != 1 || len(adminPage.UserProfileList) != 1 ||
				userdom.MustParseID(s.T(), adminPage.UserProfileList[0].UserID) != createdUserID {
				s.T().Fatalf("admin page view mismatch: %+v", adminPage)
			}

			// 视角 C：独立数据库连接回读真实持久化行，并确认存量旁观用户零变更。
			stored := userdom.QueryStoredProfile(s.T(), env, createdUserID)
			if stored.UserName != "飞行员小明" || !stored.AvatarURL.Valid || stored.AvatarURL.String != generatedURL ||
				stored.Version != 1 || !stored.IsEnabled {
				s.T().Fatalf("database stored profile mismatch: %+v", stored)
			}
			bystanderSnap.RequireUnchanged(s.T())
		})
	})

	t.Run("02_preserve_registered_account_when_avatar_generation_fails", func(t *testing.T) {
		// [产品目标]
		// 新用户注册已提交落库后，若后续 AI 生成头像阶段 Qwen SDK 发生故障（返回 500），
		// 下游公司图片服务不应被调用，也不继续执行更新；但前序已注册的账户必须完好保留初始状态且可正常查询。
		env := suite.Shared(t)
		sd := seed.New(t, env.DB)
		sd.Freeze()
		fl := harness.NewFlow(t, env, flow.WithSeed(sd))

		var (
			createdUserID int64
			issuedToken   string
		)

		fl.Step("01_register_new_user_account", func(s *flow.Step) {
			created := userdom.MustCreateUser(s, userdom.CreateUserRequest{
				AccountSource:     "wechat",
				ExternalAccountID: "wx_avatar_fail_user",
				UserName:          "待换头像的小华",
			})
			createdUserID = userdom.MustParseID(s.T(), created.UserProfile.UserID)
			issuedToken = created.AccessToken
		})

		fl.Step("02_avatar_generation_aborts_on_qwen_error", func(s *flow.Step) {
			// 仅声明 Qwen 返回限流故障；不注册 python-service-template 路由，一旦未短路误调将自动判败。
			qwenRule := s.ExpectSDK("ai.llm.qwen", "GenerateText",
				sdkmock.RespondError("UPSTREAM_RATE_LIMITED", http.StatusTooManyRequests, 4290, "rate limit exceeded"),
			)

			resp := userdom.CallGenerateAvatar(s, "赛博朋克机械猫", apicall.WithBearerToken(issuedToken))
			apicall.RequireError(s.T(), resp, http.StatusInternalServerError, "INTERNAL_ERROR", "")
			qwenRule.RequireCallCount(s.T(), 1)
		})

		fl.Step("03_verify_registered_user_remains_intact_at_initial_version", func(s *flow.Step) {
			queried := userdom.MustQueryProfileByUserID(s, createdUserID, apicall.WithBearerToken(issuedToken))
			if queried.UserName != "待换头像的小华" || queried.AvatarURL != nil || queried.Version != 0 {
				s.T().Fatalf("expected initial profile preserved after avatar failure, got: %+v", queried)
			}

			stored := userdom.QueryStoredProfile(s.T(), env, createdUserID)
			if stored.UserName != "待换头像的小华" || stored.AvatarURL.Valid || stored.Version != 0 {
				s.T().Fatalf("unexpected stored profile state: %+v", stored)
			}
		})
	})

	t.Run("03_reject_stale_version_replay_and_keep_latest_profile", func(t *testing.T) {
		// [产品目标]
		// 用户完成首次头像设置（版本 0 -> 1）后，若多端缓存未刷新，携带旧版本 0 提交第二次生成的头像，
		// 更新接口必须返回 409 USER_PROFILE_VERSION_CONFLICT，且线上查询与数据库均保持第一次成功提交的资料。
		env := suite.Shared(t)
		sd := seed.New(t, env.DB)
		sd.Freeze()
		fl := harness.NewFlow(t, env, flow.WithSeed(sd))

		const firstAvatarURL = "https://cdn.example.com/generated/avatar-v1.png"
		const secondAvatarURL = "https://cdn.example.com/generated/avatar-v2-stale.png"

		var (
			createdUserID int64
			issuedToken   string
			staleVersion  int64
			v1AvatarURL   string
			v2AvatarURL   string
		)

		fl.Step("01_register_and_record_initial_version", func(s *flow.Step) {
			created := userdom.MustCreateUser(s, userdom.CreateUserRequest{
				AccountSource:     "github",
				ExternalAccountID: "gh_replay_guard_01",
				UserName:          "版本测试员V0",
			})
			createdUserID = userdom.MustParseID(s.T(), created.UserProfile.UserID)
			issuedToken = created.AccessToken
			staleVersion = created.UserProfile.Version
		})

		fl.Step("02_generate_and_apply_first_avatar_successfully", func(s *flow.Step) {
			s.ExpectSDK("ai.llm.qwen", "GenerateText", sdkmock.RespondJSON(map[string]any{"content": "blue geometric bird"}))
			s.ExpectHTTP("python-service-template", http.MethodPost, "/api/v1/image/imageGen/generateImage",
				httpstub.JSONResponse(http.StatusOK, map[string]any{"image_url": firstAvatarURL}),
			)
			gen := userdom.MustGenerateAvatar(s, "蓝色几何飞鸟", apicall.WithBearerToken(issuedToken))
			v1AvatarURL = gen.AvatarURL

			updated := userdom.MustUpdateProfile(s, userdom.UpdateProfileRequest{
				UserID:          createdUserID,
				ExpectedVersion: staleVersion,
				UserName:        "版本测试员V1",
				AvatarURL:       &v1AvatarURL,
			}, apicall.WithBearerToken(issuedToken))
			if updated.Version != 1 {
				s.T().Fatalf("updated.Version = %d, want 1", updated.Version)
			}
		})

		fl.Step("03_generate_second_avatar_but_replay_stale_version_zero", func(s *flow.Step) {
			s.ExpectSDK("ai.llm.qwen", "GenerateText", sdkmock.RespondJSON(map[string]any{"content": "red origami fox"}))
			s.ExpectHTTP("python-service-template", http.MethodPost, "/api/v1/image/imageGen/generateImage",
				httpstub.JSONResponse(http.StatusOK, map[string]any{"image_url": secondAvatarURL}),
			)
			gen2 := userdom.MustGenerateAvatar(s, "红色折纸狐狸", apicall.WithBearerToken(issuedToken))
			v2AvatarURL = gen2.AvatarURL

			conflictResp := userdom.CallUpdateProfile(s, userdom.UpdateProfileRequest{
				UserID:          createdUserID,
				ExpectedVersion: staleVersion, // 故意重放过期版本 0
				UserName:        "被拒绝的覆盖昵称",
				AvatarURL:       &v2AvatarURL,
			}, apicall.WithBearerToken(issuedToken))
			apicall.RequireError(s.T(), conflictResp, http.StatusConflict, "CONFLICT", "USER_PROFILE_VERSION_CONFLICT")
		})

		fl.Step("04_confirm_v1_profile_preserved_in_api_and_db", func(s *flow.Step) {
			latest := userdom.MustQueryProfileByUserID(s, createdUserID, apicall.WithBearerToken(issuedToken))
			if latest.UserName != "版本测试员V1" || latest.AvatarURL == nil || *latest.AvatarURL != firstAvatarURL || latest.Version != 1 {
				s.T().Fatalf("expected V1 profile preserved, got: %+v", latest)
			}

			stored := userdom.QueryStoredProfile(s.T(), env, createdUserID)
			if stored.UserName != "版本测试员V1" || stored.AvatarURL.String != firstAvatarURL || stored.Version != 1 {
				s.T().Fatalf("expected V1 stored row preserved, got: %+v", stored)
			}
		})
	})

	t.Run("04_forbid_applying_generated_avatar_to_other_user", func(t *testing.T) {
		// [产品目标]
		// 新用户 Alice 注册并生成自己的头像后，若篡改请求中的目标 user_id 试图覆盖存量用户 Bob 的资料，
		// 必须被对象级权限拦截（403 USER_PROFILE_PERMISSION_DENIED），且 Alice 与 Bob 的资料均保持各自应有状态。
		env := suite.Shared(t)
		sd := seed.New(t, env.DB)
		fl := harness.NewFlow(t, env, flow.WithSeed(sd))

		bobID := sd.ID("user:target_bob")
		userdom.InsertProfile(sd, userdom.ProfileSeed{
			ID:                bobID,
			AccountSource:     "wechat",
			ExternalAccountID: "wx_target_bob",
			UserName:          "无辜的目标Bob",
			AvatarURL:         sql.NullString{String: "https://cdn.example.com/avatars/bob-original.png", Valid: true},
			IsEnabled:         true,
			Version:           0,
			CreatedAt:         sd.Time(-12 * time.Hour),
		})
		sd.Freeze()

		bobSnapBefore := dbassert.CaptureSingleRow(t, env.DB(t, "user"),
			"SELECT user_name, avatar_url, version FROM user_profile WHERE id = ?", bobID)

		var (
			aliceID      int64
			aliceToken   string
			generatedURL string
		)

		fl.Step("01_alice_registers_and_generates_avatar", func(s *flow.Step) {
			created := userdom.MustCreateUser(s, userdom.CreateUserRequest{
				AccountSource:     "wechat",
				ExternalAccountID: "wx_attacker_alice",
				UserName:          "新用户Alice",
			})
			aliceID = userdom.MustParseID(s.T(), created.UserProfile.UserID)
			aliceToken = created.AccessToken

			s.ExpectSDK("ai.llm.qwen", "GenerateText", sdkmock.RespondJSON(map[string]any{"content": "pixel art knight"}))
			s.ExpectHTTP("python-service-template", http.MethodPost, "/api/v1/image/imageGen/generateImage",
				httpstub.JSONResponse(http.StatusOK, map[string]any{"image_url": "https://cdn.example.com/generated/knight.png"}),
			)
			generatedURL = userdom.MustGenerateAvatar(s, "像素风骑士", apicall.WithBearerToken(aliceToken)).AvatarURL
		})

		fl.Step("02_alice_attempts_cross_user_update_against_bob", func(s *flow.Step) {
			resp := userdom.CallUpdateProfile(s, userdom.UpdateProfileRequest{
				UserID:          bobID, // 越权指定 Bob 的 user_id
				ExpectedVersion: 0,
				UserName:        "被篡改的Bob",
				AvatarURL:       &generatedURL,
			}, apicall.WithBearerToken(aliceToken))
			apicall.RequireError(s.T(), resp, http.StatusForbidden, "PERMISSION_DENIED", "USER_PROFILE_PERMISSION_DENIED")
		})

		fl.Step("03_verify_both_alice_and_bob_profiles_remain_untouched", func(s *flow.Step) {
			bobSnapBefore.RequireUnchanged(s.T())

			aliceView := userdom.MustQueryProfileByUserID(s, aliceID, apicall.WithBearerToken(aliceToken))
			if aliceView.UserName != "新用户Alice" || aliceView.AvatarURL != nil || aliceView.Version != 0 {
				s.T().Fatalf("unexpected Alice profile state: %+v", aliceView)
			}

			adminToken := env.Auth.IssueServiceAdminToken(s.T(), "security-audit")
			batch := userdom.MustBatchQueryProfiles(s, []int64{bobID, aliceID}, apicall.WithBearerToken(adminToken))
			if len(batch.UserProfileList) != 2 ||
				batch.UserProfileList[0].UserName != "无辜的目标Bob" ||
				batch.UserProfileList[1].UserName != "新用户Alice" {
				s.T().Fatalf("unexpected batch profile check: %+v", batch)
			}
		})
	})

	t.Run("05_isolate_company_image_timeout_without_polluting_next_flow", func(t *testing.T) {
		// [产品目标]
		// 当公司内部图片生成服务响应慢于客户端超时预算时，生成头像失败中断；
		// 按规范使用 Isolated 独占环境承载慢下游残留请求，验证已注册账户完好保留初始态并安全销毁独占进程。
		env := suite.Isolated(t, harness.WithCompanyRequestTimeout(150*time.Millisecond))
		sd := seed.New(t, env.DB)
		sd.Freeze()
		fl := harness.NewFlow(t, env, flow.WithSeed(sd))

		var (
			createdUserID int64
			issuedToken   string
		)

		fl.Step("01_register_user_in_isolated_environment", func(s *flow.Step) {
			created := userdom.MustCreateUser(s, userdom.CreateUserRequest{
				AccountSource:     "wechat",
				ExternalAccountID: "wx_timeout_isolated",
				UserName:          "慢下游测试用户",
			})
			createdUserID = userdom.MustParseID(s.T(), created.UserProfile.UserID)
			issuedToken = created.AccessToken
		})

		fl.Step("02_company_image_service_exceeds_request_timeout", func(s *flow.Step) {
			s.ExpectSDK("ai.llm.qwen", "GenerateText", sdkmock.RespondJSON(map[string]any{"content": "oil painting lighthouse"}))
			s.ExpectHTTP("python-service-template", http.MethodPost, "/api/v1/image/imageGen/generateImage",
				httpstub.DelayedResponse(350*time.Millisecond, httpstub.JSONResponse(http.StatusOK, map[string]any{
					"image_url": "https://cdn.example.com/generated/late-lighthouse.png",
				})),
			)

			resp := userdom.CallGenerateAvatar(s, "油画灯塔", apicall.WithBearerToken(issuedToken))
			apicall.RequireError(s.T(), resp, http.StatusInternalServerError, "INTERNAL_ERROR", "")
		})

		fl.Step("03_verify_profile_remained_at_initial_state", func(s *flow.Step) {
			selfProf := userdom.MustQueryProfileByUserID(s, createdUserID, apicall.WithBearerToken(issuedToken))
			if selfProf.AvatarURL != nil || selfProf.Version != 0 {
				s.T().Fatalf("profile should remain unchanged after image timeout, got: %+v", selfProf)
			}
		})
	})
}
