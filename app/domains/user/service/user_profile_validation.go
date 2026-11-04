package service

import (
	"math"
	"net/url"
	"strings"
	"unicode/utf8"

	usererrors "go-service-template/app/domains/user/errors"
)

// validateText 只校验原值，不通过 trim、大小写转换等方式改变身份或展示内容。
func validateText(field, value string, maximum int) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > maximum {
		return &usererrors.InvalidArgumentError{Field: field, Reason: "must be non-blank UTF-8 text within the allowed length"}
	}
	return nil
}

func validateProfile(userName string, avatarURL *string) error {
	if err := validateText("user_name", userName, 64); err != nil {
		return err
	}
	if avatarURL == nil || *avatarURL == "" {
		return nil
	}
	parsed, err := url.Parse(*avatarURL)
	if !utf8.ValidString(*avatarURL) || utf8.RuneCountInString(*avatarURL) > 512 || err != nil ||
		parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return &usererrors.InvalidArgumentError{Field: "avatar_url", Reason: "must be null, empty, or an absolute HTTP(S) URL of at most 512 characters"}
	}
	return nil
}

func validateCreateUser(params CreateUserParams) error {
	if err := validateText("account_source", params.AccountSource, 32); err != nil {
		return err
	}
	if err := validateText("external_account_id", params.ExternalAccountID, 128); err != nil {
		return err
	}
	return validateProfile(params.UserName, params.AvatarURL)
}

func validateUpdateUserProfile(params UpdateUserProfileParams) error {
	if params.OperatorUserID <= 0 {
		return &usererrors.InvalidArgumentError{Field: "operator_user_id", Reason: "must be a positive 64-bit integer"}
	}
	if params.UserID <= 0 {
		return usererrors.ErrInvalidUserID
	}
	if params.ExpectedVersion < 0 || params.ExpectedVersion == math.MaxInt64 {
		return &usererrors.InvalidArgumentError{Field: "expected_version", Reason: "must be between 0 and 9223372036854775806"}
	}
	if params.OperatorUserID != params.UserID {
		return usererrors.ErrUserProfilePermissionDenied
	}
	return validateProfile(params.UserName, params.AvatarURL)
}

func validateBatchUserIDs(userIDs []int64) error {
	if len(userIDs) == 0 || len(userIDs) > MaxUserProfileBatchSize {
		return &usererrors.InvalidArgumentError{Field: "user_id_list", Reason: "must contain between 1 and 100 user IDs"}
	}
	for _, id := range userIDs {
		if id <= 0 {
			return &usererrors.InvalidArgumentError{Field: "user_id_list", Reason: "every user ID must be a positive 64-bit integer"}
		}
	}
	return nil
}

func validateUserProfilePage(params QueryUserProfilePageParams) error {
	if params.PageSize <= 0 || params.PageSize > MaxUserProfilePageSize {
		return &usererrors.InvalidArgumentError{Field: "page_size", Reason: "must be between 1 and 100"}
	}
	if params.PageNumber <= 0 || params.PageNumber-1 > math.MaxInt/params.PageSize {
		return &usererrors.InvalidArgumentError{Field: "page_number", Reason: "must be a positive page number with a supported offset"}
	}
	if params.UserName != "" {
		return validateText("user_name", params.UserName, 64)
	}
	return nil
}
