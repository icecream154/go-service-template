//go:build business

// 产品流程业务测试：跨领域存量会员权益巡检与订单关联变更历史追溯
// 主属业务域      ：user（跨领域前置关联：order_service 会员订单 + post_service 社区发帖点赞 + user_service 资料/关注/权益/流水）
// 涉及数据库表    ：
//   - user_service.user_profile
//   - user_service.user_follow
//   - user_service.user_membership
//   - user_service.membership_change_record
//   - order_service.membership_order
//   - post_service.post
//   - post_service.post_like
// 串联 HTTP 接口  ：
//   1. GET  /api/v1/user/userProfile/queryUserProfileByUserId
//   2. GET  /api/v1/user/userMembership/queryUserMembershipByUserId
//   3. GET  /api/v1/user/userMembership/queryMembershipChangeRecordPage（连续多页传递真实 next_page_token）
//   4. POST /api/v1/user/userProfile/batchQueryUserProfiles
//
// 产品用例与变体总览（共 2 个流程场景）：
//   01_active_member_inspects_profile_benefits_and_paginates_order_history  [主流程]   结合 testdata 模板与 seed.Session 构造三库 7 表存量生态，验证有效会员状态、未支付订单隔离及同毫秒多页游标追溯全链路零写副作用
//   02_expired_member_retains_historical_ledger_and_admin_cross_check       [到期分支] 历史权益已到期且存在待支付新订单时，本人端展示 EXPIRED 快照并可完整追溯历史流水，管理端批量巡检状态一致
package user_business_test

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go-service-template/tests/business/domains/orderdom"
	"go-service-template/tests/business/domains/postdom"
	"go-service-template/tests/business/domains/userdom"
	"go-service-template/tests/harness"
	"go-service-template/tests/testkit/apicall"
	"go-service-template/tests/testkit/dbassert"
	"go-service-template/tests/testkit/flow"
	"go-service-template/tests/testkit/seed"
)

type membershipLedgerFixture struct {
	PlanCode           string `json:"plan_code"`
	PlanVersion        int64  `json:"plan_version"`
	MembershipLevel    int32  `json:"membership_level"`
	DurationDays       int    `json:"duration_days"`
	PayableAmountCents int64  `json:"payable_amount_cents"`
	PaymentChannel     string `json:"payment_channel"`
	Orders             []struct {
		Alias         string `json:"alias"`
		OrderNo       string `json:"order_no"`
		TransactionNo string `json:"transaction_no"`
		ChangeAlias   string `json:"change_alias"`
		ChangeType    int32  `json:"change_type"`
	} `json:"orders"`
	CommunityPost struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	} `json:"community_post"`
}

func TestInspectCrossDomainMembershipLifecycle(t *testing.T) {
	t.Run("01_active_member_inspects_profile_benefits_and_paginates_order_history", func(t *testing.T) {
		// [产品目标]
		// 模拟一位拥有完整社区互动数据（关注、发帖、获赞）及多阶段会员购买履约历史（首购开通 + 同毫秒双单续费 + 一笔待支付未履约新单）的资深会员，
		// 登录后依次查看个人资料、当前会员有效期窗口（确认待支付新单未提前累加权益），并按每页 1 条持真实 next_page_token 翻完 3 页变更流水，
		// 最终确认所有流水均与 order_service.membership_order 已履约订单严格对应，且整套巡检对三库 7 表零写副作用。
		env := suite.Shared(t)
		sd := seed.New(t, env.DB)
		fl := harness.NewFlow(t, env, flow.WithSeed(sd))

		// ==================== [1. 前置跨领域存量状态（Given Cross-Domain State）] ====================
		tpl := seed.LoadJSON[membershipLedgerFixture](t, filepath.Join("testdata", "membership_ledger_fixture.json"))
		planDuration := time.Duration(tpl.DurationDays) * 24 * time.Hour

		tOpen := sd.Time(-20 * 24 * time.Hour)
		tRenewSameMS := sd.Time(-5 * 24 * time.Hour) // 第 2、3 笔订单在同一 UTC 毫秒完成履约，检验游标 (created_at DESC, id DESC) 决胜
		tPending := sd.Time(-1 * time.Hour)

		windowEnd1 := tOpen.Add(planDuration)
		windowEnd2 := windowEnd1.Add(planDuration)
		windowEnd3 := windowEnd2.Add(planDuration) // 当前生效快照应停留在第 3 笔已履约订单累加后的到期时间

		aliceID := sd.ID("user:alice")
		fanID := sd.ID("user:fan_bob")
		membershipID := sd.ID("membership:alice")

		// 1.1 user_service：用户资料与关注关系
		userdom.InsertProfile(sd, userdom.ProfileSeed{
			ID:                aliceID,
			AccountSource:     "wechat",
			ExternalAccountID: "wx_senior_alice",
			UserName:          "资深创作者Alice",
			AvatarURL:         sql.NullString{String: "https://cdn.example.com/avatars/alice.png", Valid: true},
			IsEnabled:         true,
			Version:           3,
			CreatedAt:         sd.Time(-60 * 24 * time.Hour),
		})
		userdom.InsertProfile(sd, userdom.ProfileSeed{
			ID:                fanID,
			AccountSource:     "wechat",
			ExternalAccountID: "wx_fan_bob",
			UserName:          "关注者Bob",
			IsEnabled:         true,
			CreatedAt:         sd.Time(-30 * 24 * time.Hour),
		})
		userdom.InsertFollow(sd, userdom.FollowSeed{
			ID:             sd.ID("follow:bob_to_alice"),
			FollowerUserID: fanID,
			FollowedUserID: aliceID,
			CreatedAt:      sd.Time(-15 * 24 * time.Hour),
		})

		// 1.2 post_service：Alice 的社区帖子与获赞记录（like_count 与 post_like 行数严格自洽）
		postdom.InsertPost(sd, postdom.PostSeed{
			ID:           sd.ID("post:alice_share"),
			AuthorUserID: aliceID,
			Title:        tpl.CommunityPost.Title,
			Content:      tpl.CommunityPost.Content,
			LikeCount:    1,
			CreatedAt:    sd.Time(-10 * 24 * time.Hour),
		})
		postdom.InsertPostLike(sd, postdom.PostLikeSeed{
			ID:        sd.ID("like:bob_likes_alice_post"),
			PostID:    sd.ID("post:alice_share"),
			UserID:    fanID,
			CreatedAt: sd.Time(-9 * 24 * time.Hour),
		})

		// 1.3 order_service：3 笔已支付履约会员订单 + 1 笔待支付未履约订单
		paidTimes := []time.Time{tOpen, tRenewSameMS, tRenewSameMS}
		for i, ord := range tpl.Orders {
			paidAt := paidTimes[i]
			orderdom.InsertMembershipOrder(sd, orderdom.MembershipOrderSeed{
				ID:                   sd.ID(ord.Alias),
				OrderNo:              ord.OrderNo,
				UserID:               aliceID,
				PlanCode:             tpl.PlanCode,
				PlanVersion:          tpl.PlanVersion,
				MembershipLevel:      tpl.MembershipLevel,
				DurationSeconds:      int64(planDuration.Seconds()),
				PayableAmount:        tpl.PayableAmountCents,
				PaymentStatus:        orderdom.PaymentStatusPaidAndFulfilled,
				PaymentChannel:       tpl.PaymentChannel,
				PaymentTransactionNo: sql.NullString{String: ord.TransactionNo, Valid: true},
				PaidAt:               sql.NullTime{Time: paidAt, Valid: true},
				FulfilledAt:          sql.NullTime{Time: paidAt, Valid: true},
				CreatedAt:            paidAt.Add(-2 * time.Minute),
			})
		}
		// 第 4 笔待支付订单（不应产生变更流水，也不应延长当前会员有效期）
		orderdom.InsertMembershipOrder(sd, orderdom.MembershipOrderSeed{
			ID:              sd.ID("order:unpaid_pending"),
			OrderNo:         "MO202611139999",
			UserID:          aliceID,
			PlanCode:        tpl.PlanCode,
			PlanVersion:     tpl.PlanVersion,
			MembershipLevel: tpl.MembershipLevel,
			DurationSeconds: int64(planDuration.Seconds()),
			PayableAmount:   tpl.PayableAmountCents,
			PaymentStatus:   orderdom.PaymentStatusPending,
			PaymentChannel:  tpl.PaymentChannel,
			CreatedAt:       tPending,
		})

		// 1.4 user_service：当前会员权益快照与 3 条一一关联已履约订单的变更流水
		// 预先按时间顺序分配 change ID，保证同毫秒下第 3 笔续费 ID > 第 2 笔续费 ID。
		chgID1 := sd.ID(tpl.Orders[0].ChangeAlias)
		chgID2 := sd.ID(tpl.Orders[1].ChangeAlias)
		chgID3 := sd.ID(tpl.Orders[2].ChangeAlias)

		userdom.InsertMembership(sd, userdom.MembershipSeed{
			ID:              membershipID,
			UserID:          aliceID,
			MembershipLevel: tpl.MembershipLevel,
			EffectiveAt:     tOpen,
			ExpiredAt:       windowEnd3,
			CreatedAt:       tOpen,
			UpdatedAt:       tRenewSameMS,
		})
		userdom.InsertChangeRecord(sd, userdom.ChangeRecordSeed{
			ID:                   chgID1,
			UserID:               aliceID,
			MembershipID:         membershipID,
			OrderID:              sd.ID(tpl.Orders[0].Alias),
			ChangeType:           tpl.Orders[0].ChangeType,
			AfterMembershipLevel: tpl.MembershipLevel,
			AfterEffectiveAt:     tOpen,
			AfterExpiredAt:       windowEnd1,
			CreatedAt:            tOpen,
		})
		userdom.InsertChangeRecord(sd, userdom.ChangeRecordSeed{
			ID:                    chgID2,
			UserID:                aliceID,
			MembershipID:          membershipID,
			OrderID:               sd.ID(tpl.Orders[1].Alias),
			ChangeType:            tpl.Orders[1].ChangeType,
			BeforeMembershipLevel: sql.NullInt32{Int32: tpl.MembershipLevel, Valid: true},
			BeforeEffectiveAt:     sql.NullTime{Time: tOpen, Valid: true},
			BeforeExpiredAt:       sql.NullTime{Time: windowEnd1, Valid: true},
			AfterMembershipLevel:  tpl.MembershipLevel,
			AfterEffectiveAt:      tOpen,
			AfterExpiredAt:        windowEnd2,
			CreatedAt:             tRenewSameMS,
		})
		userdom.InsertChangeRecord(sd, userdom.ChangeRecordSeed{
			ID:                    chgID3,
			UserID:                aliceID,
			MembershipID:          membershipID,
			OrderID:               sd.ID(tpl.Orders[2].Alias),
			ChangeType:            tpl.Orders[2].ChangeType,
			BeforeMembershipLevel: sql.NullInt32{Int32: tpl.MembershipLevel, Valid: true},
			BeforeEffectiveAt:     sql.NullTime{Time: tOpen, Valid: true},
			BeforeExpiredAt:       sql.NullTime{Time: windowEnd2, Valid: true},
			AfterMembershipLevel:  tpl.MembershipLevel,
			AfterEffectiveAt:      tOpen,
			AfterExpiredAt:        windowEnd3,
			CreatedAt:             tRenewSameMS,
		})
		sd.Freeze()

		// 捕获三库全部 7 张表的行数基线，用于结束时证明整轮巡检无任何脏写副作用。
		allTableCounts := dbassert.CaptureCounts(t, env.DB,
			dbassert.Table("user", "user_profile"),
			dbassert.Table("user", "user_follow"),
			dbassert.Table("user", "user_membership"),
			dbassert.Table("user", "membership_change_record"),
			dbassert.Table("order", "membership_order"),
			dbassert.Table("post", "post"),
			dbassert.Table("post", "post_like"),
		)
		aliceAuth := apicall.WithBearerToken(env.Auth.IssueMemberToken(t, aliceID))

		// ==================== [2. 跨步骤流转变量（Flow Handoff State）] ====================
		var (
			inspectedUserID int64
			page1NextToken  string
			page2NextToken  string
			seenOrderIDs    []int64
		)

		// ==================== [3. 有序产品步骤推进（Ordered Steps）] ====================
		fl.Step("01_member_queries_own_profile", func(s *flow.Step) {
			prof := userdom.MustQueryProfileByUserID(s, aliceID, aliceAuth)
			if prof.UserName != "资深创作者Alice" || !prof.IsEnabled || prof.Version != 3 {
				s.T().Fatalf("unexpected profile: %+v", prof)
			}
			inspectedUserID = userdom.MustParseID(s.T(), prof.UserID)
		})

		fl.Step("02_member_verifies_active_membership_window", func(s *flow.Step) {
			mem := userdom.MustQueryMembershipByUserID(s, inspectedUserID, aliceAuth)
			if mem.MembershipStatus != "ACTIVE" || mem.Membership == nil {
				s.T().Fatalf("expected ACTIVE membership, got status=%q body=%+v", mem.MembershipStatus, mem)
			}
			if mem.Membership.MembershipLevel != tpl.MembershipLevel ||
				!mem.Membership.EffectiveAt.Equal(tOpen) ||
				!mem.Membership.ExpiredAt.Equal(windowEnd3) {
				s.T().Fatalf("membership window = [%v, %v), want [%v, %v) (pending order must not extend expiry)",
					mem.Membership.EffectiveAt, mem.Membership.ExpiredAt, tOpen, windowEnd3)
			}
		})

		fl.Step("03_paginate_change_history_page_1_latest_same_ms_renewal", func(s *flow.Step) {
			p1 := userdom.MustQueryMembershipChangeRecordPage(s, userdom.QueryMembershipChangeRecordPageRequest{
				UserID:   inspectedUserID,
				PageSize: 1,
			}, aliceAuth)
			if len(p1.MembershipChangeRecordList) != 1 || !p1.HasMore || p1.NextPageToken == nil {
				s.T().Fatalf("expected page 1 with 1 item and next_page_token, got: %+v", p1)
			}
			rec := p1.MembershipChangeRecordList[0]
			if userdom.MustParseID(s.T(), rec.RecordID) != chgID3 || !rec.AfterExpiredAt.Equal(windowEnd3) {
				s.T().Fatalf("page 1 record = %+v, want chgID3 (%d)", rec, chgID3)
			}
			seenOrderIDs = append(seenOrderIDs, userdom.MustParseID(s.T(), rec.OrderID))
			page1NextToken = *p1.NextPageToken
		})

		fl.Step("04_paginate_change_history_page_2_using_real_cursor_tie_breaker", func(s *flow.Step) {
			p2 := userdom.MustQueryMembershipChangeRecordPage(s, userdom.QueryMembershipChangeRecordPageRequest{
				UserID:    inspectedUserID,
				PageSize:  1,
				PageToken: page1NextToken,
			}, aliceAuth)
			if len(p2.MembershipChangeRecordList) != 1 || !p2.HasMore || p2.NextPageToken == nil {
				s.T().Fatalf("expected page 2 with 1 item and next_page_token, got: %+v", p2)
			}
			rec := p2.MembershipChangeRecordList[0]
			if userdom.MustParseID(s.T(), rec.RecordID) != chgID2 || !rec.AfterExpiredAt.Equal(windowEnd2) {
				s.T().Fatalf("page 2 record = %+v, want chgID2 (%d)", rec, chgID2)
			}
			seenOrderIDs = append(seenOrderIDs, userdom.MustParseID(s.T(), rec.OrderID))
			page2NextToken = *p2.NextPageToken
		})

		fl.Step("05_paginate_change_history_page_3_initial_opening_and_eof", func(s *flow.Step) {
			p3 := userdom.MustQueryMembershipChangeRecordPage(s, userdom.QueryMembershipChangeRecordPageRequest{
				UserID:    inspectedUserID,
				PageSize:  1,
				PageToken: page2NextToken,
			}, aliceAuth)
			if len(p3.MembershipChangeRecordList) != 1 || p3.HasMore || p3.NextPageToken != nil {
				s.T().Fatalf("expected final page 3 with has_more=false and nil next_page_token, got: %+v", p3)
			}
			rec := p3.MembershipChangeRecordList[0]
			if userdom.MustParseID(s.T(), rec.RecordID) != chgID1 ||
				rec.ChangeType != 10 ||
				rec.BeforeMembershipLevel != nil ||
				!rec.AfterExpiredAt.Equal(windowEnd1) {
				s.T().Fatalf("page 3 initial opening record mismatch: %+v", rec)
			}
			seenOrderIDs = append(seenOrderIDs, userdom.MustParseID(s.T(), rec.OrderID))
		})

		// ==================== [4. 最终跨库关联闭环与零写副作用核对（End-State Audit）] ====================
		fl.Step("06_audit_cross_domain_order_links_and_zero_mutations", func(s *flow.Step) {
			wantOrderIDs := []int64{
				sd.ID(tpl.Orders[2].Alias),
				sd.ID(tpl.Orders[1].Alias),
				sd.ID(tpl.Orders[0].Alias),
			}
			if len(seenOrderIDs) != len(wantOrderIDs) {
				s.T().Fatalf("seen order IDs = %v, want %v", seenOrderIDs, wantOrderIDs)
			}
			// 逐笔核对变更流水指向的 order_service.membership_order 均为已支付已履约订单（排除未支付订单）。
			for i, orderID := range seenOrderIDs {
				if orderID != wantOrderIDs[i] {
					s.T().Fatalf("order[%d] = %d, want %d", i, orderID, wantOrderIDs[i])
				}
				storedOrder := orderdom.QueryStoredMembershipOrder(s.T(), env, orderID)
				if storedOrder.UserID != aliceID || storedOrder.PaymentStatus != orderdom.PaymentStatusPaidAndFulfilled {
					s.T().Fatalf("linked order %d invalid state: %+v", orderID, storedOrder)
				}
			}

			// 确认三库 7 张表在全流程只读巡检后行数无任何增删。
			allTableCounts.RequireUnchanged(s.T())
		})
	})

	t.Run("02_expired_member_retains_historical_ledger_and_admin_cross_check", func(t *testing.T) {
		// [产品目标]
		// 历史会员已过到期时间且用户名下存在一笔尚未支付的新续费订单时：
		// 1. 本人查询会员状态返回 EXPIRED，保留历史权益快照且未被待支付订单激活；
		// 2. 本人查询变更记录仍能完整看到历史首次开通流水；
		// 3. 后台运营服务通过批量接口可同时读取该到期会员与未开通普通用户的账户资料。
		env := suite.Shared(t)
		sd := seed.New(t, env.DB)
		fl := harness.NewFlow(t, env, flow.WithSeed(sd))

		expiredUserID := sd.ID("user:expired_carol")
		neverOpenedUserID := sd.ID("user:free_dave")
		membershipID := sd.ID("membership:carol")
		paidOrderID := sd.ID("order:carol_hist_paid")

		histOpenAt := sd.Time(-45 * 24 * time.Hour)
		histExpiredAt := sd.Time(-15 * 24 * time.Hour)

		userdom.InsertProfile(sd, userdom.ProfileSeed{
			ID:                expiredUserID,
			AccountSource:     "wechat",
			ExternalAccountID: "wx_expired_carol",
			UserName:          "已到期会员Carol",
			IsEnabled:         true,
			CreatedAt:         sd.Time(-90 * 24 * time.Hour),
		})
		userdom.InsertProfile(sd, userdom.ProfileSeed{
			ID:                neverOpenedUserID,
			AccountSource:     "wechat",
			ExternalAccountID: "wx_free_dave",
			UserName:          "普通用户Dave",
			IsEnabled:         true,
			CreatedAt:         sd.Time(-10 * 24 * time.Hour),
		})
		orderdom.InsertMembershipOrder(sd, orderdom.MembershipOrderSeed{
			ID:                   paidOrderID,
			OrderNo:              "MO202609010088",
			UserID:               expiredUserID,
			PlanCode:             "STD_MONTH",
			PlanVersion:          1,
			MembershipLevel:      10,
			DurationSeconds:      30 * 86400,
			PayableAmount:        2990,
			PaymentStatus:        orderdom.PaymentStatusPaidAndFulfilled,
			PaymentChannel:       "alipay",
			PaymentTransactionNo: sql.NullString{String: "ALI_TX_0088", Valid: true},
			PaidAt:               sql.NullTime{Time: histOpenAt, Valid: true},
			FulfilledAt:          sql.NullTime{Time: histOpenAt, Valid: true},
			CreatedAt:            histOpenAt,
		})
		orderdom.InsertMembershipOrder(sd, orderdom.MembershipOrderSeed{
			ID:              sd.ID("order:carol_unpaid_renew"),
			OrderNo:         "MO202611130089",
			UserID:          expiredUserID,
			PlanCode:        "STD_MONTH",
			PlanVersion:     1,
			MembershipLevel: 10,
			DurationSeconds: 30 * 86400,
			PayableAmount:   2990,
			PaymentStatus:   orderdom.PaymentStatusPending,
			PaymentChannel:  "alipay",
			CreatedAt:       sd.Time(-30 * time.Minute),
		})
		userdom.InsertMembership(sd, userdom.MembershipSeed{
			ID:              membershipID,
			UserID:          expiredUserID,
			MembershipLevel: 10,
			EffectiveAt:     histOpenAt,
			ExpiredAt:       histExpiredAt,
		})
		userdom.InsertChangeRecord(sd, userdom.ChangeRecordSeed{
			ID:                   sd.ID("change:carol_open"),
			UserID:               expiredUserID,
			MembershipID:         membershipID,
			OrderID:              paidOrderID,
			ChangeType:           10,
			AfterMembershipLevel: 10,
			AfterEffectiveAt:     histOpenAt,
			AfterExpiredAt:       histExpiredAt,
			CreatedAt:            histOpenAt,
		})
		sd.Freeze()

		carolSnap := dbassert.CaptureSingleRow(t, env.DB(t, "user"),
			"SELECT membership_level, effective_at, expired_at FROM user_membership WHERE user_id = ?",
			expiredUserID,
		)
		carolAuth := apicall.WithBearerToken(env.Auth.IssueMemberToken(t, expiredUserID))
		daveAuth := apicall.WithBearerToken(env.Auth.IssueMemberToken(t, neverOpenedUserID))

		fl.Step("01_carol_sees_expired_status_with_retained_snapshot", func(s *flow.Step) {
			mem := userdom.MustQueryMembershipByUserID(s, expiredUserID, carolAuth)
			if mem.MembershipStatus != "EXPIRED" || mem.Membership == nil {
				s.T().Fatalf("expected EXPIRED with retained snapshot, got: %+v", mem)
			}
			if !mem.Membership.ExpiredAt.Equal(histExpiredAt) {
				s.T().Fatalf("expired_at = %v, want %v", mem.Membership.ExpiredAt, histExpiredAt)
			}
		})

		fl.Step("02_carol_can_still_inspect_historical_change_ledger", func(s *flow.Step) {
			history := userdom.MustQueryMembershipChangeRecordPage(s, userdom.QueryMembershipChangeRecordPageRequest{
				UserID:   expiredUserID,
				PageSize: 10,
			}, carolAuth)
			if len(history.MembershipChangeRecordList) != 1 || history.HasMore ||
				history.MembershipChangeRecordList[0].OrderID != strconv.FormatInt(paidOrderID, 10) {
				s.T().Fatalf("unexpected historical ledger for expired member: %+v", history)
			}
		})

		fl.Step("03_dave_sees_not_opened_and_admin_batch_queries_both_users", func(s *flow.Step) {
			daveMem := userdom.MustQueryMembershipByUserID(s, neverOpenedUserID, daveAuth)
			if daveMem.MembershipStatus != "NOT_OPENED" || daveMem.Membership != nil {
				s.T().Fatalf("expected NOT_OPENED for Dave, got: %+v", daveMem)
			}

			adminToken := env.Auth.IssueServiceAdminToken(s.T(), "crm-batch-inspector")
			batch := userdom.MustBatchQueryProfiles(s, []int64{expiredUserID, neverOpenedUserID}, apicall.WithBearerToken(adminToken))
			if len(batch.UserProfileList) != 2 || len(batch.MissingUserIDList) != 0 {
				s.T().Fatalf("unexpected admin batch result: %+v", batch)
			}

			// 确认已过期的会员记录在查询后仍原样保留在数据库中，未被静默删除或篡改。
			carolSnap.RequireUnchanged(s.T())
		})
	})
}
