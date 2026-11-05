package models

import (
	usererrors "go-service-template/app/domains/user/errors"
	checkformat "go-service-template/internal/check/format"
	checktext "go-service-template/internal/check/text"
)

const (
	// MaxAccountSourceRuneCount 是账户命名空间的 Unicode 码点数上限，允许等于该值。
	// 公开该元数据供接口适配层生成规则说明，校验仍调用 ValidateAccountSource。
	MaxAccountSourceRuneCount = 32
	// MaxExternalAccountIDRuneCount 是外部账户标识的 Unicode 码点数上限，允许等于该值。
	MaxExternalAccountIDRuneCount = 128
	// MaxUserNameRuneCount 是展示昵称的 Unicode 码点数上限，允许等于该值。
	MaxUserNameRuneCount = 64
	// MaxAvatarURLRuneCount 是头像地址的 Unicode 码点数上限，允许等于该值。
	MaxAvatarURLRuneCount = 512
)

// ValidateAccountSource 检查账户命名空间为非空白 UTF-8 文本且不超过长度上限。
// 保留原始大小写及首尾空白，不访问外部身份系统。
func ValidateAccountSource(value string) error {
	return validateProfileText(usererrors.FieldAccountSource, value, MaxAccountSourceRuneCount)
}

// ValidateExternalAccountID 检查外部账户标识为非空白 UTF-8 文本且不超过长度上限。
// 本规则只保证字段值合法，不判断身份真实性或账户是否已经存在。
func ValidateExternalAccountID(value string) error {
	return validateProfileText(usererrors.FieldExternalAccountID, value, MaxExternalAccountIDRuneCount)
}

// ValidateUserName 检查展示昵称为非空白 UTF-8 文本且不超过长度上限。
// 昵称可重复，不在检查中裁剪空白或转换字符。
func ValidateUserName(value string) error {
	return validateProfileText(usererrors.FieldUserName, value, MaxUserNameRuneCount)
}

// ValidateAvatarURL 检查一个已提供的头像地址，要求非空白 UTF-8 文本、长度有效，
// 且为包含主机名的绝对 HTTP/HTTPS URL；不访问图片或限制来源域名。
// 是否允许省略头像或空字符串由对应接口及用例决定，需要跳过时由调用方处理。
func ValidateAvatarURL(value string) error {
	if err := validateProfileText(usererrors.FieldAvatarURL, value, MaxAvatarURLRuneCount); err != nil {
		return err
	}
	if _, err := checkformat.ParseHTTPURL(value); err != nil {
		return &usererrors.FieldValidationError{Field: usererrors.FieldAvatarURL, Err: err}
	}
	return nil
}

// validateProfileText 组合编码、非空白和码点数检查，不改变字段值。
func validateProfileText(field usererrors.ValidationField, value string, maximum int) error {
	if err := checktext.NotBlank(value); err != nil {
		return &usererrors.FieldValidationError{Field: field, Err: err}
	}
	if err := checktext.RuneCountAtMost(value, maximum); err != nil {
		return &usererrors.FieldValidationError{Field: field, Err: err}
	}
	return nil
}
