package service

import (
	"fmt"
	"math"

	usererrors "go-service-template/app/domains/user/errors"
	"go-service-template/app/domains/user/models"
	"go-service-template/internal/check/collection"
	"go-service-template/internal/check/relation"
	"go-service-template/internal/check/scalar"
)

// validateCreateUser 组织创建用例的必填与可选输入，字段自身规则统一交给 models。
// 未提供头像和显式空字符串均允许，保留原始值，不补默认值或修改 Params。
func validateCreateUser(params CreateUserParams) error {
	if err := models.ValidateAccountSource(params.AccountSource); err != nil {
		return err
	}
	if err := models.ValidateExternalAccountID(params.ExternalAccountID); err != nil {
		return err
	}
	if err := models.ValidateUserName(params.UserName); err != nil {
		return err
	}
	if params.AvatarURL == nil || *params.AvatarURL == "" {
		return nil
	}
	if err := models.ValidateAvatarURL(*params.AvatarURL); err != nil {
		return err
	}
	return nil
}

// validateUpdateUserProfile 检查更新用例的纯输入约束，不读取权限、状态或实际版本。
// 本用例完整替换昵称与头像，头像必须提供且不能清空。
func validateUpdateUserProfile(params UpdateUserProfileParams) error {
	if err := scalar.PositiveInt64(params.OperatorUserID); err != nil {
		return &usererrors.InvalidArgumentError{Field: "operator_user_id", Reason: "must be a positive 64-bit integer"}
	}
	if err := scalar.PositiveInt64(params.UserID); err != nil {
		return usererrors.ErrInvalidUserID
	}
	if err := scalar.Int64InRange(params.ExpectedVersion, MinUserProfileExpectedVersion, MaxUserProfileExpectedVersion); err != nil {
		return &usererrors.InvalidArgumentError{Field: "expected_version", Reason: fmt.Sprintf("must be between %d and %d", MinUserProfileExpectedVersion, MaxUserProfileExpectedVersion)}
	}
	if err := models.ValidateUserName(params.UserName); err != nil {
		return err
	}
	if err := relation.NotNil(params.AvatarURL); err != nil {
		return &usererrors.InvalidArgumentError{Field: "avatar_url", Reason: "must not be blank"}
	}
	if err := models.ValidateAvatarURL(*params.AvatarURL); err != nil {
		return err
	}
	return nil
}

// validateBatchUserIDs 在去重前检查提交数量，再逐项检查已经映射成整数的 ID。
// 集合检查不重排、不去重，也不把整数转换回 HTTP 文本重新解析。
func validateBatchUserIDs(userIDs []int64) error {
	if err := collection.SliceLengthInRange(userIDs, MinUserProfileBatchSize, MaxUserProfileBatchSize); err != nil {
		return &usererrors.InvalidArgumentError{Field: "user_id_list", Reason: fmt.Sprintf("must contain between %d and %d user IDs", MinUserProfileBatchSize, MaxUserProfileBatchSize)}
	}
	if err := collection.Each(userIDs, scalar.PositiveInt64); err != nil {
		return &usererrors.InvalidArgumentError{Field: "user_id_list", Reason: "every user ID must be a positive 64-bit integer"}
	}
	return nil
}

// validateUserProfilePage 先保证页码及每页条数有效，再检查乘法偏移范围与可选筛选值。
// 默认值由调用入口准备，显式零值不在 Service 内自动改写。
func validateUserProfilePage(params QueryUserProfilePageParams) error {
	if err := scalar.IntInRange(params.PageSize, MinUserProfilePageSize, MaxUserProfilePageSize); err != nil {
		return &usererrors.InvalidArgumentError{Field: "page_size", Reason: fmt.Sprintf("must be between %d and %d", MinUserProfilePageSize, MaxUserProfilePageSize)}
	}
	if err := scalar.IntAtLeast(params.PageNumber, MinUserProfilePageNumber); err != nil {
		return &usererrors.InvalidArgumentError{Field: "page_number", Reason: "must be a positive page number with a supported offset"}
	}
	if err := scalar.IntAtMost(params.PageNumber-1, math.MaxInt/params.PageSize); err != nil {
		return &usererrors.InvalidArgumentError{Field: "page_number", Reason: "must be a positive page number with a supported offset"}
	}
	if params.UserName != "" {
		if err := models.ValidateUserName(params.UserName); err != nil {
			return err
		}
	}
	return nil
}
